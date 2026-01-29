package rabbitmq

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/rapidataai/rabbitmq-burst-scaler/internal/config"
)

// Consumer represents a RabbitMQ consumer for a specific ScaledObject.
type Consumer struct {
	conn             *amqp.Connection
	channel          *amqp.Channel
	scaledObjectName string
	namespace        string
	queueName        string
	stateManager     *StateManager
	onBurst          func() // Callback for push notifications
	logger           *slog.Logger
	cancel           context.CancelFunc
	done             chan struct{}
}

// ConsumerManager manages consumers for multiple ScaledObjects.
type ConsumerManager struct {
	consumers map[string]*Consumer // key: namespace/name
	mu        sync.RWMutex
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

// GetOrCreateConsumer returns an existing consumer or creates a new one.
func (m *ConsumerManager) GetOrCreateConsumer(
	ctx context.Context,
	scaledObjectName, namespace string,
	cfg *config.TriggerConfig,
	stateManager *StateManager,
	onBurst func(), // Callback for push notifications
) (*Consumer, error) {
	key := consumerKey(namespace, scaledObjectName)

	m.mu.RLock()
	if consumer, ok := m.consumers[key]; ok {
		m.mu.RUnlock()
		return consumer, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if consumer, ok := m.consumers[key]; ok {
		return consumer, nil
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
	conn, err := amqp.Dial(cfg.AMQPURL())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	queueName := sourceQueueName(scaledObjectName, namespace)

	// Declare the source queue
	_, err = ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,
	)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("failed to declare source queue: %w", err)
	}

	// Bind to the exchange with the routing key
	err = ch.QueueBind(
		queueName,
		cfg.RoutingKey,
		cfg.Exchange,
		false,
		nil,
	)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("failed to bind queue to exchange: %w", err)
	}

	consumerCtx, cancel := context.WithCancel(context.Background())

	consumer := &Consumer{
		conn:             conn,
		channel:          ch,
		scaledObjectName: scaledObjectName,
		namespace:        namespace,
		queueName:        queueName,
		stateManager:     stateManager,
		onBurst:          onBurst,
		logger:           m.logger.With("scaledObject", scaledObjectName, "namespace", namespace),
		cancel:           cancel,
		done:             make(chan struct{}),
	}

	// Ensure state queue exists
	if err := stateManager.EnsureStateQueue(ctx, scaledObjectName, namespace, cfg.BurstDuration); err != nil {
		_ = consumer.Close()
		return nil, fmt.Errorf("failed to ensure state queue: %w", err)
	}

	// Start consuming
	go consumer.consume(consumerCtx)

	m.logger.Info("created consumer", "scaledObject", scaledObjectName, "namespace", namespace, "queue", queueName)
	return consumer, nil
}

// consume processes messages from the source queue.
func (c *Consumer) consume(ctx context.Context) {
	defer close(c.done)

	deliveries, err := c.channel.Consume(
		c.queueName,
		"",    // consumer tag (auto-generated)
		false, // autoAck
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,
	)
	if err != nil {
		c.logger.Error("failed to start consuming", "error", err)
		return
	}

	c.logger.Info("started consuming messages")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("consumer stopped")
			return
		case delivery, ok := <-deliveries:
			if !ok {
				c.logger.Warn("delivery channel closed")
				return
			}

			c.logger.Debug("received message", "routingKey", delivery.RoutingKey)

			// Trigger burst
			if err := c.stateManager.TriggerBurst(ctx, c.scaledObjectName, c.namespace); err != nil {
				c.logger.Error("failed to trigger burst", "error", err)
				// Nack and requeue
				if nackErr := delivery.Nack(false, true); nackErr != nil {
					c.logger.Error("failed to nack message", "error", nackErr)
				}
				continue
			}

			// Notify listeners for push-based scaling
			if c.onBurst != nil {
				c.onBurst()
			}

			// Ack the message
			if ackErr := delivery.Ack(false); ackErr != nil {
				c.logger.Error("failed to ack message", "error", ackErr)
			}
		}
	}
}

// Close stops the consumer and cleans up resources.
func (c *Consumer) Close() error {
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for consumer goroutine to finish
	select {
	case <-c.done:
	default:
	}

	var errs []error

	if c.channel != nil {
		if err := c.channel.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing consumer: %v", errs)
	}
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
