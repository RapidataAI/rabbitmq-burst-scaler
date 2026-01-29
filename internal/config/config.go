package config

import (
	"errors"
	"fmt"
	"strconv"
)

// TriggerConfig holds the configuration parsed from ScaledObject trigger metadata.
type TriggerConfig struct {
	// RabbitMQ connection
	Host     string
	Port     int
	Username string
	Password string
	Vhost    string

	// Source configuration
	Exchange   string
	RoutingKey string

	// Burst configuration
	BurstReplicas        int
	BurstDurationMinutes int
}

// ParseTriggerMetadata parses the trigger metadata from a ScaledObject into TriggerConfig.
func ParseTriggerMetadata(metadata map[string]string) (*TriggerConfig, error) {
	config := &TriggerConfig{
		Port:  5672,
		Vhost: "/",
	}

	// Required fields
	var ok bool

	if config.Host, ok = metadata["host"]; !ok || config.Host == "" {
		return nil, errors.New("host is required")
	}

	if config.Exchange, ok = metadata["exchange"]; !ok || config.Exchange == "" {
		return nil, errors.New("exchange is required")
	}

	if config.RoutingKey, ok = metadata["routingKey"]; !ok || config.RoutingKey == "" {
		return nil, errors.New("routingKey is required")
	}

	burstReplicasStr, ok := metadata["burstReplicas"]
	if !ok || burstReplicasStr == "" {
		return nil, errors.New("burstReplicas is required")
	}
	burstReplicas, err := strconv.Atoi(burstReplicasStr)
	if err != nil {
		return nil, fmt.Errorf("burstReplicas must be a valid integer: %w", err)
	}
	if burstReplicas < 1 {
		return nil, errors.New("burstReplicas must be at least 1")
	}
	config.BurstReplicas = burstReplicas

	burstDurationStr, ok := metadata["burstDurationMinutes"]
	if !ok || burstDurationStr == "" {
		return nil, errors.New("burstDurationMinutes is required")
	}
	burstDuration, err := strconv.Atoi(burstDurationStr)
	if err != nil {
		return nil, fmt.Errorf("burstDurationMinutes must be a valid integer: %w", err)
	}
	if burstDuration < 1 {
		return nil, errors.New("burstDurationMinutes must be at least 1")
	}
	config.BurstDurationMinutes = burstDuration

	// Optional fields
	if port, ok := metadata["port"]; ok && port != "" {
		portNum, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("port must be a valid integer: %w", err)
		}
		config.Port = portNum
	}

	if vhost, ok := metadata["vhost"]; ok && vhost != "" {
		config.Vhost = vhost
	}

	// Credentials (may come from TriggerAuthentication)
	config.Username = metadata["username"]
	config.Password = metadata["password"]

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
