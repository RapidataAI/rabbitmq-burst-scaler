package scaler

import (
	"context"
	"log/slog"
	"os"
	"testing"

	pb "github.com/rapidataai/rabbitmq-burst-scaler/proto"
)

func clearRabbitMQEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("RABBITMQ_HOST", "")
	t.Setenv("RABBITMQ_PORT", "")
	t.Setenv("RABBITMQ_USERNAME", "")
	t.Setenv("RABBITMQ_PASSWORD", "")
	t.Setenv("RABBITMQ_VHOST", "")
}

func setRabbitMQEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("RABBITMQ_HOST", "rabbitmq.default")
	t.Setenv("RABBITMQ_USERNAME", "guest")
	t.Setenv("RABBITMQ_PASSWORD", "guest")
}

func TestScaledObjectKey(t *testing.T) {
	tests := []struct {
		namespace string
		name      string
		expected  string
	}{
		{"default", "my-scaler", "default/my-scaler"},
		{"keda-system", "burst-scaler", "keda-system/burst-scaler"},
		{"", "test", "/test"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := scaledObjectKey(tt.namespace, tt.name)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestBurstScalerValidation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	s := New(logger)
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	tests := []struct {
		name     string
		setupEnv func(t *testing.T)
		ref      *pb.ScaledObjectRef
		wantErr  bool
		errMatch string
	}{
		{
			name:     "missing RABBITMQ_HOST env var",
			setupEnv: clearRabbitMQEnvVars,
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"exchange":      "events",
					"routingKey":    "jobs.created",
					"burstReplicas": "5",
					"burstDuration": "2m",
				},
			},
			wantErr:  true,
			errMatch: "RABBITMQ_HOST environment variable is required",
		},
		{
			name:     "missing exchange",
			setupEnv: setRabbitMQEnvVars,
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"routingKey":    "jobs.created",
					"burstReplicas": "5",
					"burstDuration": "2m",
				},
			},
			wantErr:  true,
			errMatch: "exchange is required",
		},
		{
			name:     "missing routingKey",
			setupEnv: setRabbitMQEnvVars,
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"exchange":      "events",
					"burstReplicas": "5",
					"burstDuration": "2m",
				},
			},
			wantErr:  true,
			errMatch: "routingKey is required",
		},
		{
			name:     "missing burstReplicas",
			setupEnv: setRabbitMQEnvVars,
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"exchange":      "events",
					"routingKey":    "jobs.created",
					"burstDuration": "2m",
				},
			},
			wantErr:  true,
			errMatch: "burstReplicas is required",
		},
		{
			name:     "missing burstDuration",
			setupEnv: setRabbitMQEnvVars,
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"exchange":      "events",
					"routingKey":    "jobs.created",
					"burstReplicas": "5",
				},
			},
			wantErr:  true,
			errMatch: "burstDuration is required",
		},
		{
			name:     "invalid burstReplicas",
			setupEnv: setRabbitMQEnvVars,
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"exchange":      "events",
					"routingKey":    "jobs.created",
					"burstReplicas": "invalid",
					"burstDuration": "2m",
				},
			},
			wantErr:  true,
			errMatch: "burstReplicas must be a valid integer",
		},
		{
			name:     "invalid burstDuration",
			setupEnv: setRabbitMQEnvVars,
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"exchange":      "events",
					"routingKey":    "jobs.created",
					"burstReplicas": "5",
					"burstDuration": "invalid",
				},
			},
			wantErr:  true,
			errMatch: "burstDuration must be a valid duration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupEnv(t)

			_, err := s.IsActive(ctx, tt.ref)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
					return
				}
				if tt.errMatch != "" && !containsString(err.Error(), tt.errMatch) {
					t.Errorf("expected error containing %q, got %q", tt.errMatch, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
