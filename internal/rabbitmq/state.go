package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// StateManager manages burst state queues with TTL.
type StateManager struct {
	amqpURL string
	conn    *amqp.Connection
	channel *amqp.Channel
	mu      sync.Mutex
	logger  *slog.Logger
}

// NewStateManager creates a new state manager connected to RabbitMQ.
func NewStateManager(amqpURL string, logger *slog.Logger) (*StateManager, error) {
	s := &StateManager{
		amqpURL: amqpURL,
		logger:  logger,
	}

	if err := s.connect(); err != nil {
		return nil, err
	}

	return s, nil
}

// connect establishes a connection and channel to RabbitMQ.
func (s *StateManager) connect() error {
	conn, err := amqp.Dial(s.amqpURL)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}

	s.conn = conn
	s.channel = ch
	s.logger.Info("connected to RabbitMQ")
	return nil
}

// reconnect closes any existing connection and establishes a new one.
func (s *StateManager) reconnect() error {
	if s.channel != nil {
		_ = s.channel.Close()
		s.channel = nil
	}
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}

	s.logger.Info("reconnecting to RabbitMQ")
	return s.connect()
}

// isConnectionError checks if the error indicates a closed connection/channel.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var amqpErr *amqp.Error
	if errors.As(err, &amqpErr) {
		// Channel/connection closed errors
		return amqpErr.Code == amqp.ChannelError || amqpErr.Code == amqp.ConnectionForced
	}
	// Check for common connection error messages
	errStr := err.Error()
	return errors.Is(err, amqp.ErrClosed) ||
		strings.Contains(errStr, "channel/connection is not open") ||
		strings.Contains(errStr, "connection closed") ||
		strings.Contains(errStr, "EOF")
}

// stateQueueName returns the name of the state queue for a ScaledObject.
func stateQueueName(scaledObjectName, namespace string) string {
	return fmt.Sprintf("burst-state-%s-%s", namespace, scaledObjectName)
}

// EnsureStateQueue creates or updates the state queue with the specified TTL.
func (s *StateManager) EnsureStateQueue(ctx context.Context, scaledObjectName, namespace string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queueName := stateQueueName(scaledObjectName, namespace)
	ttlMs := int32(ttl.Milliseconds())

	args := amqp.Table{
		"x-message-ttl": ttlMs,
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
		if isConnectionError(err) {
			s.logger.Warn("connection error, attempting reconnect", "error", err)
			if reconnErr := s.reconnect(); reconnErr != nil {
				return fmt.Errorf("failed to reconnect: %w (original error: %v)", reconnErr, err)
			}
			_, err = s.channel.QueueDeclare(
				queueName,
				true,
				false,
				false,
				false,
				args,
			)
			if err != nil {
				return fmt.Errorf("failed to declare state queue after reconnect: %w", err)
			}
		} else {
			return fmt.Errorf("failed to declare state queue: %w", err)
		}
	}

	s.logger.Debug("ensured state queue", "queue", queueName, "ttl", ttl)
	return nil
}

// TriggerBurst publishes a burst marker to the state queue.
// Due to max-length=1 and drop-head, this replaces any existing marker.
func (s *StateManager) TriggerBurst(ctx context.Context, scaledObjectName, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queueName := stateQueueName(scaledObjectName, namespace)

	publishing := amqp.Publishing{
		ContentType:  "text/plain",
		Body:         []byte(time.Now().UTC().Format(time.RFC3339)),
		DeliveryMode: amqp.Persistent,
	}

	err := s.channel.PublishWithContext(
		ctx,
		"",        // default exchange
		queueName, // routing key = queue name
		false,     // mandatory
		false,     // immediate
		publishing,
	)
	if err != nil {
		if isConnectionError(err) {
			s.logger.Warn("connection error, attempting reconnect", "error", err)
			if reconnErr := s.reconnect(); reconnErr != nil {
				return fmt.Errorf("failed to reconnect: %w (original error: %v)", reconnErr, err)
			}
			err = s.channel.PublishWithContext(
				ctx,
				"",
				queueName,
				false,
				false,
				publishing,
			)
			if err != nil {
				return fmt.Errorf("failed to publish burst marker after reconnect: %w", err)
			}
		} else {
			return fmt.Errorf("failed to publish burst marker: %w", err)
		}
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

		// Try to reconnect on connection errors
		if isConnectionError(err) {
			s.logger.Warn("connection error, attempting reconnect", "error", err)
			if reconnErr := s.reconnect(); reconnErr != nil {
				return false, fmt.Errorf("failed to reconnect: %w (original error: %v)", reconnErr, err)
			}
			// Retry the operation after reconnection
			queue, err = s.channel.QueueDeclarePassive(
				queueName,
				true,
				false,
				false,
				false,
				nil,
			)
			if err != nil {
				var amqpErr *amqp.Error
				if ok := errors.As(err, &amqpErr); ok && amqpErr.Code == amqp.NotFound {
					return false, nil
				}
				return false, fmt.Errorf("failed to inspect state queue after reconnect: %w", err)
			}
		} else {
			return false, fmt.Errorf("failed to inspect state queue: %w", err)
		}
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
		if isConnectionError(err) {
			s.logger.Warn("connection error, attempting reconnect", "error", err)
			if reconnErr := s.reconnect(); reconnErr != nil {
				return fmt.Errorf("failed to reconnect: %w (original error: %v)", reconnErr, err)
			}
			_, err = s.channel.QueueDelete(queueName, false, false, false)
			if err != nil {
				var amqpErr *amqp.Error
				if ok := errors.As(err, &amqpErr); ok && amqpErr.Code == amqp.NotFound {
					return nil
				}
				return fmt.Errorf("failed to delete state queue after reconnect: %w", err)
			}
		} else {
			return fmt.Errorf("failed to delete state queue: %w", err)
		}
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
