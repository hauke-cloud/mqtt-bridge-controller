package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func newTestManager(onStateChange StateChangeFunc) *Manager {
	col := metrics.NewCollector(prometheus.NewRegistry())
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewManager(log, col, func(_ string, _ []byte) {}, onStateChange)
}

func TestManager_EnsureAndRemove(t *testing.T) {
	mgr := newTestManager(nil)
	ctx := context.Background()

	spec := bridge.BridgeSpec{
		Name:       "test",
		Namespace:  "default",
		Host:       "192.0.2.1",
		Port:       1883,
		ClientID:   "test",
		MaxBackoff: 1 * time.Second,
	}

	// First call creates the bridge.
	if err := mgr.EnsureBridge(ctx, spec); err != nil {
		t.Fatalf("EnsureBridge: %v", err)
	}

	// Stats should now be available.
	if _, err := mgr.Stats("test", "default"); err != nil {
		t.Fatalf("Stats after EnsureBridge: %v", err)
	}

	// AllStats should include the bridge.
	all := mgr.AllStats()
	if len(all) != 1 {
		t.Fatalf("AllStats: expected 1, got %d", len(all))
	}

	// Remove.
	if err := mgr.RemoveBridge(ctx, "test", "default"); err != nil {
		t.Fatalf("RemoveBridge: %v", err)
	}

	if _, err := mgr.Stats("test", "default"); !errors.Is(err, bridge.ErrBridgeNotFound) {
		t.Errorf("expected ErrBridgeNotFound after removal, got %v", err)
	}
}

func TestManager_EnsureBridge_NoOpOnSameConfig(t *testing.T) {
	mgr := newTestManager(nil)
	ctx := context.Background()

	spec := bridge.BridgeSpec{
		Name:       "idempotent",
		Namespace:  "default",
		Host:       "192.0.2.1",
		Port:       1883,
		ClientID:   "idempotent",
		MaxBackoff: 1 * time.Second,
	}

	if err := mgr.EnsureBridge(ctx, spec); err != nil {
		t.Fatal(err)
	}
	before, _ := mgr.Stats("idempotent", "default")

	// Same config — should be a no-op (no new client).
	if err := mgr.EnsureBridge(ctx, spec); err != nil {
		t.Fatal(err)
	}
	after, _ := mgr.Stats("idempotent", "default")

	// Reconnect counter should not have changed (not restarted).
	if before.ReconnectCount != after.ReconnectCount {
		t.Errorf("bridge was restarted on identical config")
	}

	mgr.StopAll()
}

func TestManager_RemoveBridge_NotFound(t *testing.T) {
	mgr := newTestManager(nil)
	ctx := context.Background()

	err := mgr.RemoveBridge(ctx, "ghost", "default")
	if !errors.Is(err, bridge.ErrBridgeNotFound) {
		t.Errorf("expected ErrBridgeNotFound, got %v", err)
	}
}

func TestManager_SetStateChangeCallback(t *testing.T) {
	called := make(chan string, 1)
	mgr := newTestManager(nil)

	// Set callback after construction.
	mgr.SetStateChangeCallback(func(name, _ string, _ bridge.BridgeStats) {
		called <- name
	})

	// Trigger it manually (simulate internal call).
	mgr.currentOnStateChange("my-bridge", "default", bridge.BridgeStats{State: bridge.StateError})

	select {
	case name := <-called:
		if name != "my-bridge" {
			t.Errorf("expected my-bridge, got %s", name)
		}
	case <-time.After(time.Second):
		t.Error("callback not called")
	}
}

func TestManager_StopAll(t *testing.T) {
	mgr := newTestManager(nil)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		spec := bridge.BridgeSpec{
			Name:       "bridge",
			Namespace:  "ns",
			Host:       "192.0.2.1",
			Port:       1883,
			ClientID:   "client",
			MaxBackoff: 1 * time.Second,
		}
		spec.Name = "bridge-" + string(rune('0'+i))
		_ = mgr.EnsureBridge(ctx, spec)
	}

	mgr.StopAll()

	if n := len(mgr.AllStats()); n != 0 {
		t.Errorf("expected 0 bridges after StopAll, got %d", n)
	}
}
