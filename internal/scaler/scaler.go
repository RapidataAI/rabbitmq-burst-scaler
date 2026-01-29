package scaler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/rapidataai/rabbitmq-burst-scaler/internal/config"
	"github.com/rapidataai/rabbitmq-burst-scaler/internal/rabbitmq"
	pb "github.com/rapidataai/rabbitmq-burst-scaler/proto"
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

	// Push notification channels per ScaledObject
	notifyChans map[string][]chan struct{} // key: namespace/name
	notifyMu    sync.RWMutex
}

// New creates a new BurstScaler.
func New(logger *slog.Logger) *BurstScaler {
	return &BurstScaler{
		consumers:     rabbitmq.NewConsumerManager(logger),
		stateManagers: make(map[string]*rabbitmq.StateManager),
		configs:       make(map[string]*config.TriggerConfig),
		notifyChans:   make(map[string][]chan struct{}),
		logger:        logger,
	}
}

// scaledObjectKey returns the key for a ScaledObject.
func scaledObjectKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// NotifyBurst is called when a burst is triggered - notifies all listeners.
func (s *BurstScaler) NotifyBurst(namespace, name string) {
	key := scaledObjectKey(namespace, name)

	s.notifyMu.RLock()
	chans := s.notifyChans[key]
	s.notifyMu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- struct{}{}:
		default:
			// Channel full, skip (listener will catch up on next poll)
		}
	}
}

// subscribeToNotifications creates a channel for receiving burst notifications.
func (s *BurstScaler) subscribeToNotifications(namespace, name string) chan struct{} {
	key := scaledObjectKey(namespace, name)
	ch := make(chan struct{}, 1)

	s.notifyMu.Lock()
	s.notifyChans[key] = append(s.notifyChans[key], ch)
	s.notifyMu.Unlock()

	return ch
}

// unsubscribeFromNotifications removes a notification channel.
func (s *BurstScaler) unsubscribeFromNotifications(namespace, name string, ch chan struct{}) {
	key := scaledObjectKey(namespace, name)

	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()

	chans := s.notifyChans[key]
	for i, c := range chans {
		if c == ch {
			s.notifyChans[key] = append(chans[:i], chans[i+1:]...)
			break
		}
	}
	close(ch)
}

// getOrCreateStateManager returns an existing state manager or creates a new one.
func (s *BurstScaler) getOrCreateStateManager(amqpURL string) (*rabbitmq.StateManager, error) {
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

	stateManager, err := s.getOrCreateStateManager(amqpURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get state manager: %w", err)
	}

	// Pass the notification callback to the consumer
	onBurst := func() {
		s.NotifyBurst(ref.Namespace, ref.Name)
	}

	_, err = s.consumers.GetOrCreateConsumer(ctx, ref.Name, ref.Namespace, cfg, stateManager, onBurst)
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

	_, _, err := s.ensureConsumer(ctx, ref)
	if err != nil {
		s.logger.Error("failed to ensure consumer", "error", err)
		return nil, err
	}

	return &pb.GetMetricSpecResponse{
		MetricSpecs: []*pb.MetricSpec{
			{
				MetricName: metricName,
				TargetSize: 1, // Each unit of metric = 1 replica
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

// StreamIsActive streams IsActive responses (push-based scaling).
// Immediately notifies KEDA when a burst is triggered.
func (s *BurstScaler) StreamIsActive(ref *pb.ScaledObjectRef, stream pb.ExternalScaler_StreamIsActiveServer) error {
	s.logger.Debug("StreamIsActive called", "name", ref.Name, "namespace", ref.Namespace)

	ctx := stream.Context()

	_, stateManager, err := s.ensureConsumer(ctx, ref)
	if err != nil {
		s.logger.Error("failed to ensure consumer", "error", err)
		return err
	}

	// Subscribe to burst notifications for immediate push
	notifyCh := s.subscribeToNotifications(ref.Namespace, ref.Name)
	defer s.unsubscribeFromNotifications(ref.Namespace, ref.Name, notifyCh)

	// Send initial state
	active, err := stateManager.IsBurstActive(ctx, ref.Name, ref.Namespace)
	if err != nil {
		s.logger.Error("failed to check initial burst state", "error", err)
	} else {
		if err := stream.Send(&pb.IsActiveResponse{Result: active}); err != nil {
			s.logger.Error("failed to send initial stream response", "error", err)
			return err
		}
	}

	s.logger.Info("StreamIsActive listening for push notifications", "name", ref.Name, "namespace", ref.Namespace)

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("StreamIsActive context done", "name", ref.Name, "namespace", ref.Namespace)
			return nil

		case <-notifyCh:
			// Burst triggered - immediately notify KEDA
			s.logger.Info("push notification: burst triggered", "name", ref.Name, "namespace", ref.Namespace)

			active, err := stateManager.IsBurstActive(ctx, ref.Name, ref.Namespace)
			if err != nil {
				s.logger.Error("failed to check burst state after notification", "error", err)
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

	// Close all notification channels
	s.notifyMu.Lock()
	for _, chans := range s.notifyChans {
		for _, ch := range chans {
			close(ch)
		}
	}
	s.notifyChans = make(map[string][]chan struct{})
	s.notifyMu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("errors closing scaler: %v", errs)
	}
	return nil
}
