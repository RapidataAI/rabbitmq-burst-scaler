package rabbitmq

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/rapidataai/rabbitmq-burst-scaler/internal/config"
)

// Backoff bounds for consumer reconnection attempts.
const (
	reconnectBaseBackoff = 1 * time.Second
	reconnectMaxBackoff  = 30 * time.Second
)

// Consumer represents a RabbitMQ consumer for a specific ScaledObject.
type Consumer struct {
	scaledObjectName string
	namespace        string
	queueName        string
	cfg              *config.TriggerConfig
	stateManager     *StateManager
	onBurst          func() // Callback for push notifications
	logger           *slog.Logger
	cancel           context.CancelFunc
	done             chan struct{}

	mu      sync.Mutex
	conn    *amqp.Connection
	channel *amqp.Channel
}

// ConsumerManager manages consumers for multiple ScaledObjects.
type ConsumerManager struct {
	consumers map[string]*Consumer // key: namespace/name
	mu        sync.Mutex
	logger    *slog.Logger
}

// NewConsumerManager creates a new consumer manager.
func NewConsumerManager(logger *slog.Logger) *ConsumerManager {
	return &ConsumerManager{
		consumers: make(map[string]*Consumer),
		logger:    logger,
	}
}

// consumerKey returns the key for a consumer in the map.
func consumerKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// sourceQueueName returns the name of the source queue for a ScaledObject.
func sourceQueueName(scaledObjectName, namespace string) string {
	return fmt.Sprintf("burst-source-%s-%s", namespace, scaledObjectName)
}

// GetOrCreateConsumer returns a live consumer for the ScaledObject, replacing any
// cached consumer whose goroutine has exited.
func (m *ConsumerManager) GetOrCreateConsumer(
	ctx context.Context,
	scaledObjectName, namespace string,
	cfg *config.TriggerConfig,
	stateManager *StateManager,
	onBurst func(),
) (*Consumer, error) {
	key := consumerKey(namespace, scaledObjectName)

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.consumers[key]; ok {
		if existing.IsAlive() {
			return existing, nil
		}
		// Goroutine exited (e.g. context cancelled by Close). Evict and recreate.
		m.logger.Warn("evicting dead consumer", "scaledObject", scaledObjectName, "namespace", namespace)
		_ = existing.Close()
		delete(m.consumers, key)
	}

	consumer, err := m.createConsumer(ctx, scaledObjectName, namespace, cfg, stateManager, onBurst)
	if err != nil {
		return nil, err
	}

	m.consumers[key] = consumer
	return consumer, nil
}

// createConsumer creates a new consumer for a ScaledObject.
func (m *ConsumerManager) createConsumer(
	ctx context.Context,
	scaledObjectName, namespace string,
	cfg *config.TriggerConfig,
	stateManager *StateManager,
	onBurst func(),
) (*Consumer, error) {
	queueName := sourceQueueName(scaledObjectName, namespace)

	consumerCtx, cancel := context.WithCancel(context.Background())

	consumer := &Consumer{
		scaledObjectName: scaledObjectName,
		namespace:        namespace,
		queueName:        queueName,
		cfg:              cfg,
		stateManager:     stateManager,
		onBurst:          onBurst,
		logger:           m.logger.With("scaledObject", scaledObjectName, "namespace", namespace),
		cancel:           cancel,
		done:             make(chan struct{}),
	}

	if err := consumer.connect(); err != nil {
		cancel()
		return nil, fmt.Errorf("initial connect: %w", err)
	}

	if err := stateManager.EnsureStateQueue(ctx, scaledObjectName, namespace, cfg.BurstDuration); err != nil {
		_ = consumer.closeConn()
		cancel()
		return nil, fmt.Errorf("failed to ensure state queue: %w", err)
	}

	go consumer.run(consumerCtx)

	m.logger.Info("created consumer", "scaledObject", scaledObjectName, "namespace", namespace, "queue", queueName)
	return consumer, nil
}

// connect dials RabbitMQ and declares + binds the source queue.
// Caller must hold c.mu or be sure no other goroutine is racing on conn/channel.
func (c *Consumer) connect() error {
	conn, err := amqp.Dial(c.cfg.AMQPURL())
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open channel: %w", err)
	}

	if _, err := ch.QueueDeclare(c.queueName, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("declare queue: %w", err)
	}

	if err := ch.QueueBind(c.queueName, c.cfg.RoutingKey, c.cfg.Exchange, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("bind queue: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.channel = ch
	c.mu.Unlock()

	return nil
}

// closeConn closes the current connection and channel, if any.
func (c *Consumer) closeConn() error {
	c.mu.Lock()
	ch := c.channel
	conn := c.conn
	c.channel = nil
	c.conn = nil
	c.mu.Unlock()

	var errs []error
	if ch != nil {
		if err := ch.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if conn != nil {
		if err := conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close: %v", errs)
	}
	return nil
}

// currentChannel returns the current AMQP channel under lock.
func (c *Consumer) currentChannel() *amqp.Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channel
}

// IsAlive reports whether the consumer's goroutine is still running.
func (c *Consumer) IsAlive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// run drives the consumer goroutine. It establishes a Consume subscription and
// reconnects with exponential backoff if the channel/connection drops, so that
// transient AMQP failures don't permanently silence the consumer.
func (c *Consumer) run(ctx context.Context) {
	defer close(c.done)
	defer func() { _ = c.closeConn() }()

	backoff := reconnectBaseBackoff
	for {
		if ctx.Err() != nil {
			c.logger.Info("consumer stopped")
			return
		}

		ch := c.currentChannel()
		if ch == nil {
			if err := c.connect(); err != nil {
				c.logger.Error("reconnect failed", "error", err, "backoff", backoff)
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
			c.logger.Info("reconnected to RabbitMQ")
			backoff = reconnectBaseBackoff
			ch = c.currentChannel()
		}

		deliveries, err := ch.Consume(c.queueName, "", false, false, false, false, nil)
		if err != nil {
			c.logger.Error("failed to start consuming, will reconnect", "error", err, "backoff", backoff)
			_ = c.closeConn()
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		c.logger.Info("started consuming messages")
		backoff = reconnectBaseBackoff

		if !c.processDeliveries(ctx, deliveries) {
			return
		}

		// Deliveries channel closed: drop the dead connection and loop to reconnect.
		c.logger.Warn("delivery channel closed, attempting reconnect")
		_ = c.closeConn()
	}
}

// processDeliveries handles messages from the deliveries channel until ctx is done
// or the channel closes. Returns false when the context was cancelled (caller should
// exit), true when the channel closed and the caller should reconnect.
func (c *Consumer) processDeliveries(ctx context.Context, deliveries <-chan amqp.Delivery) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case delivery, ok := <-deliveries:
			if !ok {
				return true
			}

			c.logger.Debug("received message", "routingKey", delivery.RoutingKey)

			if err := c.stateManager.TriggerBurst(ctx, c.scaledObjectName, c.namespace); err != nil {
				c.logger.Error("failed to trigger burst", "error", err)
				if nackErr := delivery.Nack(false, true); nackErr != nil {
					c.logger.Error("failed to nack message", "error", nackErr)
				}
				continue
			}

			if c.onBurst != nil {
				c.onBurst()
			}

			if ackErr := delivery.Ack(false); ackErr != nil {
				c.logger.Error("failed to ack message", "error", ackErr)
			}
		}
	}
}

// nextBackoff doubles the backoff up to reconnectMaxBackoff.
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > reconnectMaxBackoff {
		return reconnectMaxBackoff
	}
	return d
}

// sleepCtx sleeps for d unless ctx is cancelled. Returns false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Close stops the consumer goroutine and releases its AMQP resources.
func (c *Consumer) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	<-c.done
	return nil
}

// RemoveConsumer stops and removes a consumer.
func (m *ConsumerManager) RemoveConsumer(namespace, name string) error {
	key := consumerKey(namespace, name)

	m.mu.Lock()
	consumer, ok := m.consumers[key]
	if ok {
		delete(m.consumers, key)
	}
	m.mu.Unlock()

	if ok {
		return consumer.Close()
	}
	return nil
}

// Close stops all consumers.
func (m *ConsumerManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for key, consumer := range m.consumers {
		if err := consumer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close consumer %s: %w", key, err))
		}
	}
	m.consumers = make(map[string]*Consumer)

	if len(errs) > 0 {
		return fmt.Errorf("errors closing consumers: %v", errs)
	}
	return nil
}
