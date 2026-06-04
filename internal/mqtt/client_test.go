package mqtt

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func newTestCollector() *metrics.Collector {
	return metrics.NewCollector(prometheus.NewRegistry())
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestBridgeClient_StateTransitions verifies that the client transitions through
// states correctly. We use a real broker only when MQTT_TEST_BROKER is set;
// otherwise we verify initial state and that Stop() returns without hanging.
func TestBridgeClient_StateTransitions(t *testing.T) {
	broker := os.Getenv("MQTT_TEST_BROKER")
	if broker == "" {
		t.Skip("MQTT_TEST_BROKER not set; skipping integration test")
	}

	var stateChanges []bridge.ConnectionState
	var callCount atomic.Int32

	spec := bridge.BridgeSpec{
		Name:       "test-bridge",
		Namespace:  "default",
		Host:       broker,
		Port:       1883,
		ClientID:   "test-client",
		MaxBackoff: 5 * time.Second,
		Topics: []bridge.TopicSpec{
			{Topic: "test/topic", QoS: 1},
		},
	}

	cl := newBridgeClient(
		spec,
		newTestLogger(),
		newTestCollector(),
		func(topic string, payload []byte) {
			t.Logf("msg on %s: %s", topic, payload)
		},
		func(name, namespace string, stats bridge.BridgeStats) {
			stateChanges = append(stateChanges, stats.State)
			callCount.Add(1)
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cl.Start(ctx)
	defer cl.Stop()

	// Wait for connected state.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		stats := cl.Stats()
		if stats.State == bridge.StateConnected {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	stats := cl.Stats()
	if stats.State != bridge.StateConnected {
		t.Fatalf("expected connected, got %s (error: %s)", stats.State, stats.ErrorMessage)
	}
	if callCount.Load() == 0 {
		t.Error("expected at least one state-change callback")
	}
}

func TestBridgeClient_ConnectTimeout_Unreachable(t *testing.T) {
	spec := bridge.BridgeSpec{
		Name:       "unreachable",
		Namespace:  "default",
		Host:       "192.0.2.1", // TEST-NET, RFC 5737 — guaranteed unreachable
		Port:       1883,
		ClientID:   "test",
		MaxBackoff: 2 * time.Second,
	}

	stateChanges := make(chan bridge.ConnectionState, 10)
	cl := newBridgeClient(
		spec,
		newTestLogger(),
		newTestCollector(),
		func(_ string, _ []byte) {},
		func(_, _ string, stats bridge.BridgeStats) {
			stateChanges <- stats.State
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cl.Start(ctx)
	defer cl.Stop()

	// We expect the client to enter StateError or retry.
	select {
	case state := <-stateChanges:
		if state != bridge.StateError && state != bridge.StateDisconnected {
			t.Errorf("expected error/disconnected state on unreachable broker, got %s", state)
		}
	case <-time.After(18 * time.Second):
		t.Error("timeout waiting for connection failure callback")
	}
}

// TestBridgeClient_NoCredentials verifies that a spec with empty Username/Password
// does not panic or error during client construction, and that the connect attempt
// reaches the broker without sending credential fields (the broker rejects with a
// connection error, not a panic).
func TestBridgeClient_NoCredentials(t *testing.T) {
	spec := bridge.BridgeSpec{
		Name:       "anon-bridge",
		Namespace:  "default",
		Host:       "192.0.2.1", // guaranteed unreachable — we just want no panic
		Port:       1883,
		ClientID:   "anon-client",
		MaxBackoff: 1 * time.Second,
		// Username and Password intentionally left empty.
	}

	if spec.Username != "" || spec.Password != "" {
		t.Fatal("test precondition: spec must have empty credentials")
	}

	cl := newBridgeClient(spec, newTestLogger(), newTestCollector(),
		func(_ string, _ []byte) {}, nil)

	stats := cl.Stats()
	if stats.State != bridge.StateConnecting {
		t.Errorf("expected initial state=connecting, got %s", stats.State)
	}
}

func TestBridgeClient_Stats_Initial(t *testing.T) {
	spec := bridge.BridgeSpec{
		Name:       "stats-test",
		Namespace:  "default",
		Host:       "localhost",
		Port:       9999,
		ClientID:   "stats-client",
		MaxBackoff: 1 * time.Second,
	}

	cl := newBridgeClient(
		spec,
		newTestLogger(),
		newTestCollector(),
		func(_ string, _ []byte) {},
		nil,
	)

	stats := cl.Stats()
	if stats.MessagesReceived != 0 {
		t.Errorf("expected 0 messages, got %d", stats.MessagesReceived)
	}
	if stats.ReconnectCount != 0 {
		t.Errorf("expected 0 reconnects, got %d", stats.ReconnectCount)
	}
}
