package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// StateManager manages burst state queues with TTL.
type StateManager struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	mu      sync.Mutex
	logger  *slog.Logger
}

// NewStateManager creates a new state manager connected to RabbitMQ.
func NewStateManager(amqpURL string, logger *slog.Logger) (*StateManager, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	return &StateManager{
		conn:    conn,
		channel: ch,
		logger:  logger,
	}, nil
}

// stateQueueName returns the name of the state queue for a ScaledObject.
func stateQueueName(scaledObjectName, namespace string) string {
	return fmt.Sprintf("burst-state-%s-%s", namespace, scaledObjectName)
}

// EnsureStateQueue creates or updates the state queue with the specified TTL.
func (s *StateManager) EnsureStateQueue(ctx context.Context, scaledObjectName, namespace string, ttlMinutes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queueName := stateQueueName(scaledObjectName, namespace)
	ttlMs := ttlMinutes * 60 * 1000

	args := amqp.Table{
		"x-message-ttl": int32(ttlMs),
		"x-max-length":  int32(1),
		"x-overflow":    "drop-head",
	}

	_, err := s.channel.QueueDeclare(
		queueName,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		args,
	)
	if err != nil {
		return fmt.Errorf("failed to declare state queue: %w", err)
	}

	s.logger.Debug("ensured state queue", "queue", queueName, "ttl_minutes", ttlMinutes)
	return nil
}

// TriggerBurst publishes a burst marker to the state queue.
// Due to max-length=1 and drop-head, this replaces any existing marker.
func (s *StateManager) TriggerBurst(ctx context.Context, scaledObjectName, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queueName := stateQueueName(scaledObjectName, namespace)

	err := s.channel.PublishWithContext(
		ctx,
		"",        // default exchange
		queueName, // routing key = queue name
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType:  "text/plain",
			Body:         []byte(time.Now().UTC().Format(time.RFC3339)),
			DeliveryMode: amqp.Persistent,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish burst marker: %w", err)
	}

	s.logger.Info("triggered burst", "scaledObject", scaledObjectName, "namespace", namespace)
	return nil
}

// IsBurstActive checks if there's an active burst marker in the state queue.
func (s *StateManager) IsBurstActive(ctx context.Context, scaledObjectName, namespace string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	queueName := stateQueueName(scaledObjectName, namespace)

	queue, err := s.channel.QueueDeclarePassive(
		queueName,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,
	)
	if err != nil {
		// Queue doesn't exist means no burst active
		var amqpErr *amqp.Error
		if ok := errors.As(err, &amqpErr); ok && amqpErr.Code == amqp.NotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect state queue: %w", err)
	}

	active := queue.Messages > 0
	s.logger.Debug("checked burst state", "scaledObject", scaledObjectName, "namespace", namespace, "active", active, "messages", queue.Messages)
	return active, nil
}

// DeleteStateQueue removes the state queue (cleanup on ScaledObject deletion).
func (s *StateManager) DeleteStateQueue(ctx context.Context, scaledObjectName, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queueName := stateQueueName(scaledObjectName, namespace)

	_, err := s.channel.QueueDelete(queueName, false, false, false)
	if err != nil {
		// Ignore if queue doesn't exist
		var amqpErr *amqp.Error
		if ok := errors.As(err, &amqpErr); ok && amqpErr.Code == amqp.NotFound {
			return nil
		}
		return fmt.Errorf("failed to delete state queue: %w", err)
	}

	s.logger.Info("deleted state queue", "queue", queueName)
	return nil
}

// Close closes the connection to RabbitMQ.
func (s *StateManager) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.channel != nil {
		_ = s.channel.Close()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
