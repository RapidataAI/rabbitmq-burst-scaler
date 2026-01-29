package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// TriggerConfig holds the configuration parsed from ScaledObject trigger metadata
// and environment variables.
type TriggerConfig struct {
	// RabbitMQ connection (from environment variables)
	Host     string
	Port     int
	Username string
	Password string
	Vhost    string

	// Source configuration (from metadata)
	Exchange   string
	RoutingKey string

	// Burst configuration (from metadata)
	BurstReplicas int
	BurstDuration time.Duration
}

// ParseTriggerMetadata parses the trigger metadata from a ScaledObject into TriggerConfig.
// RabbitMQ connection settings are read from environment variables, while trigger-specific
// settings (exchange, routingKey, burstReplicas, burstDuration) come from metadata.
func ParseTriggerMetadata(metadata map[string]string) (*TriggerConfig, error) {
	config := &TriggerConfig{}

	// Read RabbitMQ connection settings from environment variables
	config.Host = os.Getenv("RABBITMQ_HOST")
	if config.Host == "" {
		return nil, errors.New("RABBITMQ_HOST environment variable is required")
	}

	config.Username = os.Getenv("RABBITMQ_USERNAME")
	if config.Username == "" {
		return nil, errors.New("RABBITMQ_USERNAME environment variable is required")
	}

	config.Password = os.Getenv("RABBITMQ_PASSWORD")
	if config.Password == "" {
		return nil, errors.New("RABBITMQ_PASSWORD environment variable is required")
	}

	// Optional environment variables with defaults
	portStr := os.Getenv("RABBITMQ_PORT")
	if portStr == "" {
		config.Port = 5672
	} else {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("RABBITMQ_PORT must be a valid integer: %w", err)
		}
		config.Port = port
	}

	config.Vhost = os.Getenv("RABBITMQ_VHOST")
	if config.Vhost == "" {
		config.Vhost = "/"
	}

	// Required metadata fields
	var ok bool

	if config.Exchange, ok = metadata["exchange"]; !ok || config.Exchange == "" {
		return nil, errors.New("exchange is required in metadata")
	}

	if config.RoutingKey, ok = metadata["routingKey"]; !ok || config.RoutingKey == "" {
		return nil, errors.New("routingKey is required in metadata")
	}

	burstReplicasStr, ok := metadata["burstReplicas"]
	if !ok || burstReplicasStr == "" {
		return nil, errors.New("burstReplicas is required in metadata")
	}
	burstReplicas, err := strconv.Atoi(burstReplicasStr)
	if err != nil {
		return nil, fmt.Errorf("burstReplicas must be a valid integer: %w", err)
	}
	if burstReplicas < 1 {
		return nil, errors.New("burstReplicas must be at least 1")
	}
	config.BurstReplicas = burstReplicas

	burstDurationStr, ok := metadata["burstDuration"]
	if !ok || burstDurationStr == "" {
		return nil, errors.New("burstDuration is required in metadata")
	}
	burstDuration, err := time.ParseDuration(burstDurationStr)
	if err != nil {
		return nil, fmt.Errorf("burstDuration must be a valid duration (e.g., '2m', '30s', '1h'): %w", err)
	}
	if burstDuration < time.Second {
		return nil, errors.New("burstDuration must be at least 1s")
	}
	config.BurstDuration = burstDuration

	return config, nil
}

// AMQPURL returns the AMQP connection URL.
func (c *TriggerConfig) AMQPURL() string {
	auth := ""
	if c.Username != "" {
		auth = c.Username
		if c.Password != "" {
			auth += ":" + c.Password
		}
		auth += "@"
	}

	vhost := c.Vhost
	if vhost == "/" {
		vhost = ""
	}

	return fmt.Sprintf("amqp://%s%s:%d/%s", auth, c.Host, c.Port, vhost)
}
