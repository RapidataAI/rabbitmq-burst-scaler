package config

import (
	"testing"
	"time"
)

func clearRabbitMQEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("RABBITMQ_HOST", "")
	t.Setenv("RABBITMQ_PORT", "")
	t.Setenv("RABBITMQ_USERNAME", "")
	t.Setenv("RABBITMQ_PASSWORD", "")
	t.Setenv("RABBITMQ_VHOST", "")
}

func TestParseTriggerMetadata(t *testing.T) {
	tests := []struct {
		name      string
		envVars   map[string]string
		metadata  map[string]string
		wantErr   bool
		errString string
		validate  func(*testing.T, *TriggerConfig)
	}{
		{
			name: "valid minimal config",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "2m",
			},
			wantErr: false,
			validate: func(t *testing.T, cfg *TriggerConfig) {
				if cfg.Host != "rabbitmq.default" {
					t.Errorf("expected host rabbitmq.default, got %s", cfg.Host)
				}
				if cfg.Port != 5672 {
					t.Errorf("expected default port 5672, got %d", cfg.Port)
				}
				if cfg.Vhost != "/" {
					t.Errorf("expected default vhost /, got %s", cfg.Vhost)
				}
				if cfg.Username != "guest" {
					t.Errorf("expected username guest, got %s", cfg.Username)
				}
				if cfg.Password != "guest" {
					t.Errorf("expected password guest, got %s", cfg.Password)
				}
				if cfg.Exchange != "events" {
					t.Errorf("expected exchange events, got %s", cfg.Exchange)
				}
				if cfg.RoutingKey != "jobs.created" {
					t.Errorf("expected routingKey jobs.created, got %s", cfg.RoutingKey)
				}
				if cfg.BurstReplicas != 5 {
					t.Errorf("expected burstReplicas 5, got %d", cfg.BurstReplicas)
				}
				if cfg.BurstDuration != 2*time.Minute {
					t.Errorf("expected burstDuration 2m, got %v", cfg.BurstDuration)
				}
			},
		},
		{
			name: "valid full config with custom port and vhost",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_PORT":     "5673",
				"RABBITMQ_USERNAME": "admin",
				"RABBITMQ_PASSWORD": "secret",
				"RABBITMQ_VHOST":    "myapp",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "10",
				"burstDuration": "5m",
			},
			wantErr: false,
			validate: func(t *testing.T, cfg *TriggerConfig) {
				if cfg.Port != 5673 {
					t.Errorf("expected port 5673, got %d", cfg.Port)
				}
				if cfg.Username != "admin" {
					t.Errorf("expected username admin, got %s", cfg.Username)
				}
				if cfg.Password != "secret" {
					t.Errorf("expected password secret, got %s", cfg.Password)
				}
				if cfg.Vhost != "myapp" {
					t.Errorf("expected vhost myapp, got %s", cfg.Vhost)
				}
				if cfg.BurstReplicas != 10 {
					t.Errorf("expected burstReplicas 10, got %d", cfg.BurstReplicas)
				}
				if cfg.BurstDuration != 5*time.Minute {
					t.Errorf("expected burstDuration 5m, got %v", cfg.BurstDuration)
				}
			},
		},
		{
			name: "valid config with seconds",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "30s",
			},
			wantErr: false,
			validate: func(t *testing.T, cfg *TriggerConfig) {
				if cfg.BurstDuration != 30*time.Second {
					t.Errorf("expected burstDuration 30s, got %v", cfg.BurstDuration)
				}
			},
		},
		{
			name: "valid config with hours",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "1h",
			},
			wantErr: false,
			validate: func(t *testing.T, cfg *TriggerConfig) {
				if cfg.BurstDuration != time.Hour {
					t.Errorf("expected burstDuration 1h, got %v", cfg.BurstDuration)
				}
			},
		},
		{
			name: "valid config with combined duration",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "1h30m",
			},
			wantErr: false,
			validate: func(t *testing.T, cfg *TriggerConfig) {
				expected := time.Hour + 30*time.Minute
				if cfg.BurstDuration != expected {
					t.Errorf("expected burstDuration 1h30m, got %v", cfg.BurstDuration)
				}
			},
		},
		{
			name:    "missing RABBITMQ_HOST",
			envVars: map[string]string{},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "RABBITMQ_HOST environment variable is required",
		},
		{
			name: "missing RABBITMQ_USERNAME",
			envVars: map[string]string{
				"RABBITMQ_HOST": "rabbitmq.default",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "RABBITMQ_USERNAME environment variable is required",
		},
		{
			name: "missing RABBITMQ_PASSWORD",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "RABBITMQ_PASSWORD environment variable is required",
		},
		{
			name: "invalid RABBITMQ_PORT",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_PORT":     "invalid",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "RABBITMQ_PORT must be a valid integer",
		},
		{
			name: "missing exchange",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "exchange is required",
		},
		{
			name: "missing routingKey",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"burstReplicas": "5",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "routingKey is required",
		},
		{
			name: "missing burstReplicas",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "burstReplicas is required",
		},
		{
			name: "missing burstDuration",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
			},
			wantErr:   true,
			errString: "burstDuration is required",
		},
		{
			name: "invalid burstReplicas",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "invalid",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "burstReplicas must be a valid integer",
		},
		{
			name: "zero burstReplicas",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "0",
				"burstDuration": "2m",
			},
			wantErr:   true,
			errString: "burstReplicas must be at least 1",
		},
		{
			name: "invalid burstDuration",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "invalid",
			},
			wantErr:   true,
			errString: "burstDuration must be a valid duration",
		},
		{
			name: "burstDuration too short",
			envVars: map[string]string{
				"RABBITMQ_HOST":     "rabbitmq.default",
				"RABBITMQ_USERNAME": "guest",
				"RABBITMQ_PASSWORD": "guest",
			},
			metadata: map[string]string{
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
				"burstDuration": "500ms",
			},
			wantErr:   true,
			errString: "burstDuration must be at least 1s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all RabbitMQ env vars first
			clearRabbitMQEnvVars(t)

			// Set test-specific env vars
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg, err := ParseTriggerMetadata(tt.metadata)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errString)
					return
				}
				if tt.errString != "" && !contains(err.Error(), tt.errString) {
					t.Errorf("expected error containing %q, got %q", tt.errString, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.validate != nil {
				tt.validate(t, cfg)
			}
		})
	}
}

func TestAMQPURL(t *testing.T) {
	tests := []struct {
		name     string
		config   *TriggerConfig
		expected string
	}{
		{
			name: "basic URL",
			config: &TriggerConfig{
				Host:  "rabbitmq.default",
				Port:  5672,
				Vhost: "/",
			},
			expected: "amqp://rabbitmq.default:5672/",
		},
		{
			name: "with credentials",
			config: &TriggerConfig{
				Host:     "rabbitmq.default",
				Port:     5672,
				Username: "guest",
				Password: "secret",
				Vhost:    "/",
			},
			expected: "amqp://guest:secret@rabbitmq.default:5672/",
		},
		{
			name: "with vhost",
			config: &TriggerConfig{
				Host:     "rabbitmq.default",
				Port:     5672,
				Username: "guest",
				Password: "secret",
				Vhost:    "myapp",
			},
			expected: "amqp://guest:secret@rabbitmq.default:5672/myapp",
		},
		{
			name: "custom port",
			config: &TriggerConfig{
				Host:  "rabbitmq.default",
				Port:  5673,
				Vhost: "/",
			},
			expected: "amqp://rabbitmq.default:5673/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.AMQPURL()
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
