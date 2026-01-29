package config

import (
	"testing"
)

func TestParseTriggerMetadata(t *testing.T) {
	tests := []struct {
		name      string
		metadata  map[string]string
		wantErr   bool
		errString string
		validate  func(*testing.T, *TriggerConfig)
	}{
		{
			name: "valid minimal config",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "5",
				"burstDurationMinutes": "2",
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
				if cfg.Exchange != "events" {
					t.Errorf("expected exchange events, got %s", cfg.Exchange)
				}
				if cfg.RoutingKey != "jobs.created" {
					t.Errorf("expected routingKey jobs.created, got %s", cfg.RoutingKey)
				}
				if cfg.BurstReplicas != 5 {
					t.Errorf("expected burstReplicas 5, got %d", cfg.BurstReplicas)
				}
				if cfg.BurstDurationMinutes != 2 {
					t.Errorf("expected burstDurationMinutes 2, got %d", cfg.BurstDurationMinutes)
				}
			},
		},
		{
			name: "valid full config",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"port":                 "5673",
				"username":             "guest",
				"password":             "secret",
				"vhost":                "myapp",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "10",
				"burstDurationMinutes": "5",
			},
			wantErr: false,
			validate: func(t *testing.T, cfg *TriggerConfig) {
				if cfg.Port != 5673 {
					t.Errorf("expected port 5673, got %d", cfg.Port)
				}
				if cfg.Username != "guest" {
					t.Errorf("expected username guest, got %s", cfg.Username)
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
				if cfg.BurstDurationMinutes != 5 {
					t.Errorf("expected burstDurationMinutes 5, got %d", cfg.BurstDurationMinutes)
				}
			},
		},
		{
			name: "missing host",
			metadata: map[string]string{
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "5",
				"burstDurationMinutes": "2",
			},
			wantErr:   true,
			errString: "host is required",
		},
		{
			name: "missing exchange",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"routingKey":           "jobs.created",
				"burstReplicas":        "5",
				"burstDurationMinutes": "2",
			},
			wantErr:   true,
			errString: "exchange is required",
		},
		{
			name: "missing routingKey",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"exchange":             "events",
				"burstReplicas":        "5",
				"burstDurationMinutes": "2",
			},
			wantErr:   true,
			errString: "routingKey is required",
		},
		{
			name: "missing burstReplicas",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstDurationMinutes": "2",
			},
			wantErr:   true,
			errString: "burstReplicas is required",
		},
		{
			name: "missing burstDurationMinutes",
			metadata: map[string]string{
				"host":          "rabbitmq.default",
				"exchange":      "events",
				"routingKey":    "jobs.created",
				"burstReplicas": "5",
			},
			wantErr:   true,
			errString: "burstDurationMinutes is required",
		},
		{
			name: "invalid burstReplicas",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "invalid",
				"burstDurationMinutes": "2",
			},
			wantErr:   true,
			errString: "burstReplicas must be a valid integer",
		},
		{
			name: "zero burstReplicas",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "0",
				"burstDurationMinutes": "2",
			},
			wantErr:   true,
			errString: "burstReplicas must be at least 1",
		},
		{
			name: "invalid burstDurationMinutes",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "5",
				"burstDurationMinutes": "invalid",
			},
			wantErr:   true,
			errString: "burstDurationMinutes must be a valid integer",
		},
		{
			name: "zero burstDurationMinutes",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "5",
				"burstDurationMinutes": "0",
			},
			wantErr:   true,
			errString: "burstDurationMinutes must be at least 1",
		},
		{
			name: "invalid port",
			metadata: map[string]string{
				"host":                 "rabbitmq.default",
				"port":                 "invalid",
				"exchange":             "events",
				"routingKey":           "jobs.created",
				"burstReplicas":        "5",
				"burstDurationMinutes": "2",
			},
			wantErr:   true,
			errString: "port must be a valid integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
