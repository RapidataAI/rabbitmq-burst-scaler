package scaler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jorgelopez/rabbitmq-burst-scaler/internal/config"
	"github.com/jorgelopez/rabbitmq-burst-scaler/internal/rabbitmq"
	pb "github.com/jorgelopez/rabbitmq-burst-scaler/proto"
)

const metricName = "burst_replicas"

// BurstScaler implements the KEDA ExternalScaler gRPC interface.
type BurstScaler struct {
	pb.UnimplementedExternalScalerServer
	consumers     *rabbitmq.ConsumerManager
	stateManagers map[string]*rabbitmq.StateManager // key: amqpURL
	stateMu       sync.RWMutex
	configs       map[string]*config.TriggerConfig // key: namespace/name
	configMu      sync.RWMutex
	logger        *slog.Logger
}

// New creates a new BurstScaler.
func New(logger *slog.Logger) *BurstScaler {
	return &BurstScaler{
		consumers:     rabbitmq.NewConsumerManager(logger),
		stateManagers: make(map[string]*rabbitmq.StateManager),
		configs:       make(map[string]*config.TriggerConfig),
		logger:        logger,
	}
}

// scaledObjectKey returns the key for a ScaledObject.
func scaledObjectKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// getOrCreateStateManager returns an existing state manager or creates a new one.
func (s *BurstScaler) getOrCreateStateManager(ctx context.Context, amqpURL string) (*rabbitmq.StateManager, error) {
	s.stateMu.RLock()
	if sm, ok := s.stateManagers[amqpURL]; ok {
		s.stateMu.RUnlock()
		return sm, nil
	}
	s.stateMu.RUnlock()

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	// Double-check after acquiring write lock
	if sm, ok := s.stateManagers[amqpURL]; ok {
		return sm, nil
	}

	sm, err := rabbitmq.NewStateManager(amqpURL, s.logger)
	if err != nil {
		return nil, err
	}

	s.stateManagers[amqpURL] = sm
	return sm, nil
}

// getConfig returns the cached config for a ScaledObject.
func (s *BurstScaler) getConfig(namespace, name string) (*config.TriggerConfig, bool) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	cfg, ok := s.configs[scaledObjectKey(namespace, name)]
	return cfg, ok
}

// setConfig caches the config for a ScaledObject.
func (s *BurstScaler) setConfig(namespace, name string, cfg *config.TriggerConfig) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.configs[scaledObjectKey(namespace, name)] = cfg
}

// ensureConsumer ensures a consumer exists for the ScaledObject.
func (s *BurstScaler) ensureConsumer(ctx context.Context, ref *pb.ScaledObjectRef) (*config.TriggerConfig, *rabbitmq.StateManager, error) {
	cfg, err := config.ParseTriggerMetadata(ref.ScalerMetadata)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse trigger metadata: %w", err)
	}

	// Cache config for later use
	s.setConfig(ref.Namespace, ref.Name, cfg)

	amqpURL := cfg.AMQPURL()

	stateManager, err := s.getOrCreateStateManager(ctx, amqpURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get state manager: %w", err)
	}

	_, err = s.consumers.GetOrCreateConsumer(ctx, ref.Name, ref.Namespace, cfg, stateManager)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get or create consumer: %w", err)
	}

	return cfg, stateManager, nil
}

// IsActive checks if the scaler is active (burst is happening).
func (s *BurstScaler) IsActive(ctx context.Context, ref *pb.ScaledObjectRef) (*pb.IsActiveResponse, error) {
	s.logger.Debug("IsActive called", "name", ref.Name, "namespace", ref.Namespace)

	cfg, stateManager, err := s.ensureConsumer(ctx, ref)
	if err != nil {
		s.logger.Error("failed to ensure consumer", "error", err)
		return nil, err
	}

	active, err := stateManager.IsBurstActive(ctx, ref.Name, ref.Namespace)
	if err != nil {
		s.logger.Error("failed to check burst state", "error", err)
		return nil, fmt.Errorf("failed to check burst state: %w", err)
	}

	s.logger.Info("IsActive result", "name", ref.Name, "namespace", ref.Namespace, "active", active, "burstReplicas", cfg.BurstReplicas)

	return &pb.IsActiveResponse{
		Result: active,
	}, nil
}

// GetMetricSpec returns the metric spec for the scaler.
func (s *BurstScaler) GetMetricSpec(ctx context.Context, ref *pb.ScaledObjectRef) (*pb.GetMetricSpecResponse, error) {
	s.logger.Debug("GetMetricSpec called", "name", ref.Name, "namespace", ref.Namespace)

	cfg, _, err := s.ensureConsumer(ctx, ref)
	if err != nil {
		s.logger.Error("failed to ensure consumer", "error", err)
		return nil, err
	}

	return &pb.GetMetricSpecResponse{
		MetricSpecs: []*pb.MetricSpec{
			{
				MetricName: metricName,
				TargetSize: int64(cfg.BurstReplicas),
			},
		},
	}, nil
}

// GetMetrics returns the current metric value.
func (s *BurstScaler) GetMetrics(ctx context.Context, req *pb.GetMetricsRequest) (*pb.GetMetricsResponse, error) {
	ref := req.ScaledObjectRef
	s.logger.Debug("GetMetrics called", "name", ref.Name, "namespace", ref.Namespace)

	cfg, stateManager, err := s.ensureConsumer(ctx, ref)
	if err != nil {
		s.logger.Error("failed to ensure consumer", "error", err)
		return nil, err
	}

	active, err := stateManager.IsBurstActive(ctx, ref.Name, ref.Namespace)
	if err != nil {
		s.logger.Error("failed to check burst state", "error", err)
		return nil, fmt.Errorf("failed to check burst state: %w", err)
	}

	var metricValue int64
	if active {
		// Return burstReplicas to trigger scaling to that number
		metricValue = int64(cfg.BurstReplicas)
	} else {
		// Return 0 to scale down
		metricValue = 0
	}

	s.logger.Info("GetMetrics result", "name", ref.Name, "namespace", ref.Namespace, "active", active, "metricValue", metricValue)

	return &pb.GetMetricsResponse{
		MetricValues: []*pb.MetricValue{
			{
				MetricName:  metricName,
				MetricValue: metricValue,
			},
		},
	}, nil
}

// StreamIsActive streams IsActive responses (for push-based scaling).
func (s *BurstScaler) StreamIsActive(ref *pb.ScaledObjectRef, stream pb.ExternalScaler_StreamIsActiveServer) error {
	s.logger.Debug("StreamIsActive called", "name", ref.Name, "namespace", ref.Namespace)

	ctx := stream.Context()

	_, stateManager, err := s.ensureConsumer(ctx, ref)
	if err != nil {
		s.logger.Error("failed to ensure consumer", "error", err)
		return err
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("StreamIsActive context done", "name", ref.Name, "namespace", ref.Namespace)
			return nil
		case <-ticker.C:
			active, err := stateManager.IsBurstActive(ctx, ref.Name, ref.Namespace)
			if err != nil {
				s.logger.Error("failed to check burst state in stream", "error", err)
				continue
			}

			if err := stream.Send(&pb.IsActiveResponse{Result: active}); err != nil {
				s.logger.Error("failed to send stream response", "error", err)
				return err
			}
		}
	}
}

// Close cleans up all resources.
func (s *BurstScaler) Close() error {
	var errs []error

	if err := s.consumers.Close(); err != nil {
		errs = append(errs, err)
	}

	s.stateMu.Lock()
	for _, sm := range s.stateManagers {
		if err := sm.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	s.stateManagers = make(map[string]*rabbitmq.StateManager)
	s.stateMu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("errors closing scaler: %v", errs)
	}
	return nil
}
