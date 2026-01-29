package scaler

import (
	"context"
	"log/slog"
	"os"
	"testing"

	pb "github.com/rapidataai/rabbitmq-burst-scaler/proto"
)

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
		ref      *pb.ScaledObjectRef
		wantErr  bool
		errMatch string
	}{
		{
			name: "missing host",
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"exchange":             "events",
					"routingKey":           "jobs.created",
					"burstReplicas":        "5",
					"burstDurationMinutes": "2",
				},
			},
			wantErr:  true,
			errMatch: "host is required",
		},
		{
			name: "missing exchange",
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"host":                 "rabbitmq.default:5672",
					"routingKey":           "jobs.created",
					"burstReplicas":        "5",
					"burstDurationMinutes": "2",
				},
			},
			wantErr:  true,
			errMatch: "exchange is required",
		},
		{
			name: "missing routingKey",
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"host":                 "rabbitmq.default:5672",
					"exchange":             "events",
					"burstReplicas":        "5",
					"burstDurationMinutes": "2",
				},
			},
			wantErr:  true,
			errMatch: "routingKey is required",
		},
		{
			name: "missing burstReplicas",
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"host":                 "rabbitmq.default:5672",
					"exchange":             "events",
					"routingKey":           "jobs.created",
					"burstDurationMinutes": "2",
				},
			},
			wantErr:  true,
			errMatch: "burstReplicas is required",
		},
		{
			name: "missing burstDurationMinutes",
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"host":          "rabbitmq.default:5672",
					"exchange":      "events",
					"routingKey":    "jobs.created",
					"burstReplicas": "5",
				},
			},
			wantErr:  true,
			errMatch: "burstDurationMinutes is required",
		},
		{
			name: "invalid burstReplicas",
			ref: &pb.ScaledObjectRef{
				Name:      "test",
				Namespace: "default",
				ScalerMetadata: map[string]string{
					"host":                 "rabbitmq.default:5672",
					"exchange":             "events",
					"routingKey":           "jobs.created",
					"burstReplicas":        "invalid",
					"burstDurationMinutes": "2",
				},
			},
			wantErr:  true,
			errMatch: "burstReplicas must be a valid integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
