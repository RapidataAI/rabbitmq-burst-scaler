package rabbitmq

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/rapidataai/rabbitmq-burst-scaler/internal/config"
)

// minimalCfg points at an address that should fail to dial quickly so the
// reconnect tests don't hang on network timeouts.
var minimalCfg = config.TriggerConfig{
	Host:          "127.0.0.1",
	Port:          1, // reserved port, dial fails fast
	Username:      "guest",
	Password:      "guest",
	Vhost:         "/",
	Exchange:      "events",
	RoutingKey:    "test",
	BurstReplicas: 1,
	BurstDuration: time.Second,
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSourceQueueName(t *testing.T) {
	got := sourceQueueName("session-service-scaled-object", "rapidata")
	want := "burst-source-rapidata-session-service-scaled-object"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestConsumerIsAlive(t *testing.T) {
	done := make(chan struct{})
	c := &Consumer{done: done}

	if !c.IsAlive() {
		t.Fatal("expected new consumer with open done channel to be alive")
	}

	close(done)

	if c.IsAlive() {
		t.Fatal("expected consumer with closed done channel to be dead")
	}
}

func TestConsumerCloseWaitsForGoroutine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	stopped := make(chan struct{})

	c := &Consumer{cancel: cancel, done: done}

	// Simulate a consumer goroutine that exits when ctx is cancelled.
	go func() {
		<-ctx.Done()
		close(done)
		close(stopped)
	}()

	closeErr := make(chan error, 1)
	go func() { closeErr <- c.Close() }()

	select {
	case err := <-closeErr:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return within 1s")
	}

	select {
	case <-stopped:
	default:
		t.Fatal("Close returned before goroutine signalled done")
	}
}

func TestGetOrCreateConsumerEvictsDeadEntry(t *testing.T) {
	m := NewConsumerManager(newTestLogger())

	deadDone := make(chan struct{})
	close(deadDone) // already exited

	key := consumerKey("rapidata", "session-service-scaled-object")
	dead := &Consumer{
		scaledObjectName: "session-service-scaled-object",
		namespace:        "rapidata",
		queueName:        sourceQueueName("session-service-scaled-object", "rapidata"),
		logger:           newTestLogger(),
		cancel:           func() {},
		done:             deadDone,
	}
	m.consumers[key] = dead

	// createConsumer will try to dial RabbitMQ at an unreachable address, so we
	// expect an error — but the important behaviour is that the dead entry is
	// evicted so that a fresh attempt is made, not silently bypassed.
	_, err := m.GetOrCreateConsumer(
		context.Background(),
		"session-service-scaled-object",
		"rapidata",
		&minimalCfg,
		nil, // stateManager is unused before connect() fails
		nil,
	)
	if err == nil {
		t.Fatal("expected dial error against unreachable host, got nil")
	}

	m.mu.Lock()
	_, stillCached := m.consumers[key]
	m.mu.Unlock()
	if stillCached {
		t.Fatal("expected dead consumer to be evicted on next GetOrCreateConsumer")
	}
}
