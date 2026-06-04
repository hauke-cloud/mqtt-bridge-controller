package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"k8s.io/apimachinery/pkg/types"

	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/metrics"
)

// Manager manages MQTT connections for all MQTTBridge CRs.
type Manager struct {
	log     *slog.Logger
	metrics *metrics.Collector

	mu      sync.RWMutex
	bridges map[types.NamespacedName]*BridgeClient

	onStateChange StateChangeFunc
	msgHandler    MessageHandler
}

// NewManager creates a Manager. msgHandler is called for every message on any
// managed bridge; onStateChange is called when a bridge's connection state changes.
func NewManager(
	log *slog.Logger,
	col *metrics.Collector,
	msgHandler MessageHandler,
	onStateChange StateChangeFunc,
) *Manager {
	return &Manager{
		log:           log.With("component", "mqtt-manager"),
		metrics:       col,
		bridges:       make(map[types.NamespacedName]*BridgeClient),
		onStateChange: onStateChange,
		msgHandler:    msgHandler,
	}
}

// SetStateChangeCallback replaces the state-change callback. Safe to call after
// construction so callers can wire dependencies in any order.
func (m *Manager) SetStateChangeCallback(fn StateChangeFunc) {
	m.mu.Lock()
	m.onStateChange = fn
	m.mu.Unlock()
}

// currentOnStateChange is injected into BridgeClient. It reads the stored
// callback under RLock so the callback itself runs outside any Manager lock
// (preventing deadlocks when the callback re-enters Manager).
func (m *Manager) currentOnStateChange(name, namespace string, stats bridge.BridgeStats) {
	m.mu.RLock()
	fn := m.onStateChange
	m.mu.RUnlock()
	if fn != nil {
		fn(name, namespace, stats)
	}
}

// EnsureBridge upserts an MQTT connection for the given bridge spec.
// If the bridge already exists with the same host/port/credentials, this is a
// no-op. If the configuration changed, the old connection is replaced.
func (m *Manager) EnsureBridge(ctx context.Context, spec bridge.BridgeSpec) error {
	key := types.NamespacedName{Name: spec.Name, Namespace: spec.Namespace}

	// Determine whether we need to stop an existing client — done outside the
	// write lock to avoid deadlocking with state-change callbacks.
	var toStop *BridgeClient
	var oldHost string

	m.mu.Lock()
	existing, ok := m.bridges[key]
	if ok {
		if existing.spec.Host == spec.Host &&
			existing.spec.Port == spec.Port &&
			existing.spec.Username == spec.Username &&
			existing.spec.Password == spec.Password {
			m.mu.Unlock()
			return nil
		}
		toStop = existing
		oldHost = existing.spec.Host
		delete(m.bridges, key)
	}

	cl := newBridgeClient(spec, m.log, m.metrics, m.msgHandler, m.currentOnStateChange)
	m.bridges[key] = cl
	m.mu.Unlock()

	// Stop old client outside the lock — its goroutine may call back into Manager.
	if toStop != nil {
		toStop.Stop()
		m.metrics.DeleteBridge(spec.Name, spec.Namespace, oldHost)
	} else {
		m.metrics.IncActiveBridges()
	}

	cl.Start(ctx)

	m.log.InfoContext(ctx, "bridge upserted",
		"bridge", spec.Name, "namespace", spec.Namespace,
		"host", spec.Host, "port", spec.Port)
	return nil
}

// RemoveBridge stops and removes the MQTT connection for the given bridge.
func (m *Manager) RemoveBridge(ctx context.Context, name, namespace string) error {
	key := types.NamespacedName{Name: name, Namespace: namespace}

	m.mu.Lock()
	cl, ok := m.bridges[key]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("RemoveBridge %s/%s: %w", namespace, name, bridge.ErrBridgeNotFound)
	}
	delete(m.bridges, key)
	m.mu.Unlock()

	// Stop outside the lock — the bridge goroutine may call currentOnStateChange.
	cl.Stop()
	m.metrics.DeleteBridge(name, namespace, cl.spec.Host)

	m.log.InfoContext(ctx, "bridge removed", "bridge", name, "namespace", namespace)
	return nil
}

// Stats returns runtime stats for a specific bridge.
func (m *Manager) Stats(name, namespace string) (bridge.BridgeStats, error) {
	key := types.NamespacedName{Name: name, Namespace: namespace}

	m.mu.RLock()
	defer m.mu.RUnlock()

	cl, ok := m.bridges[key]
	if !ok {
		return bridge.BridgeStats{}, fmt.Errorf("Stats %s/%s: %w", namespace, name, bridge.ErrBridgeNotFound)
	}
	return cl.Stats(), nil
}

// AllStats returns runtime stats for all managed bridges.
func (m *Manager) AllStats() map[types.NamespacedName]bridge.BridgeStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[types.NamespacedName]bridge.BridgeStats, len(m.bridges))
	for key, cl := range m.bridges {
		result[key] = cl.Stats()
	}
	return result
}

// StopAll stops all bridge connections. Called during graceful shutdown.
func (m *Manager) StopAll() {
	m.mu.Lock()
	toStop := make([]*BridgeClient, 0, len(m.bridges))
	for key, cl := range m.bridges {
		toStop = append(toStop, cl)
		delete(m.bridges, key)
	}
	m.mu.Unlock()

	// Stop all bridges outside the lock.
	for _, cl := range toStop {
		cl.Stop()
	}
}
