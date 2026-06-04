package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/metrics"
)

// MessageHandler is called for every message received on a subscribed topic.
type MessageHandler func(topic string, payload []byte)

// StateChangeFunc is called whenever the bridge connection state changes.
type StateChangeFunc func(name, namespace string, stats bridge.BridgeStats)

// BridgeClient manages a single MQTT broker connection with exponential backoff reconnect.
type BridgeClient struct {
	spec    bridge.BridgeSpec
	log     *slog.Logger
	metrics *metrics.Collector

	mu            sync.RWMutex
	client        pahomqtt.Client
	state         bridge.ConnectionState
	msgHandler    MessageHandler
	onStateChange StateChangeFunc

	reconnectCh chan struct{} // signals run loop to reconnect

	msgCount         atomic.Int64
	reconnects       atomic.Int32
	lastConnected    *time.Time
	lastDisconnected *time.Time
	errorMessage     string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func newBridgeClient(
	spec bridge.BridgeSpec,
	log *slog.Logger,
	col *metrics.Collector,
	msgHandler MessageHandler,
	onStateChange StateChangeFunc,
) *BridgeClient {
	return &BridgeClient{
		spec:          spec,
		log:           log.With("bridge", spec.Name, "namespace", spec.Namespace),
		metrics:       col,
		state:         bridge.StateConnecting,
		msgHandler:    msgHandler,
		onStateChange: onStateChange,
		reconnectCh:   make(chan struct{}, 1),
		done:          make(chan struct{}),
	}
}

// Start connects to the MQTT broker and runs the reconnect loop in the background.
func (c *BridgeClient) Start(ctx context.Context) {
	c.ctx, c.cancel = context.WithCancel(ctx)
	go c.run()
}

// Stop gracefully disconnects and waits for the run loop to exit.
func (c *BridgeClient) Stop() {
	c.cancel()
	<-c.done
}

// Stats returns a snapshot of the current bridge statistics.
func (c *BridgeClient) Stats() bridge.BridgeStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return bridge.BridgeStats{
		State:                c.state,
		MessagesReceived:     c.msgCount.Load(),
		ReconnectCount:       c.reconnects.Load(),
		LastConnectedTime:    c.lastConnected,
		LastDisconnectedTime: c.lastDisconnected,
		ErrorMessage:         c.errorMessage,
	}
}

func (c *BridgeClient) run() {
	defer close(c.done)

	backoff := time.Second
	maxBackoff := c.spec.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 60 * time.Second
	}

	for {
		// Attempt connection.
		if err := c.connect(); err != nil {
			c.setError(err)
			c.log.WarnContext(c.ctx, "connect failed, retrying",
				"error", err, "backoff", backoff.String())

			select {
			case <-c.ctx.Done():
				return
			case <-time.After(backoff):
				backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			}
			continue
		}

		// Connected — reset backoff.
		backoff = time.Second

		// Wait for disconnect signal or stop.
		select {
		case <-c.ctx.Done():
			c.mu.RLock()
			cl := c.client
			c.mu.RUnlock()
			if cl != nil {
				cl.Disconnect(500)
			}
			return
		case <-c.reconnectCh:
			// Connection lost — reconnect with backoff.
			c.log.WarnContext(c.ctx, "connection lost, reconnecting",
				"backoff", backoff.String())
			c.metrics.IncReconnect(c.spec.Name, c.spec.Namespace)
			c.reconnects.Add(1)

			select {
			case <-c.ctx.Done():
				return
			case <-time.After(backoff):
				backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			}
		}
	}
}

func (c *BridgeClient) connect() error {
	opts := pahomqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", c.spec.Host, c.spec.Port))
	opts.SetClientID(c.spec.ClientID)
	if c.spec.Username != "" {
		opts.SetUsername(c.spec.Username)
		opts.SetPassword(c.spec.Password)
	}
	opts.SetAutoReconnect(false) // handled by our own run loop
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(10 * time.Second)
	opts.SetConnectTimeout(15 * time.Second)
	opts.SetCleanSession(true)

	opts.SetOnConnectHandler(func(_ pahomqtt.Client) {
		c.onConnected()
	})

	opts.SetConnectionLostHandler(func(_ pahomqtt.Client, err error) {
		c.onConnectionLost(err)
	})

	cl := pahomqtt.NewClient(opts)

	token := cl.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return fmt.Errorf("connect timeout after 15s")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	c.mu.Lock()
	c.client = cl
	c.mu.Unlock()

	// Subscribe to all configured topics.
	for _, t := range c.spec.Topics {
		if err := c.subscribe(cl, t.Topic, t.QoS); err != nil {
			c.log.ErrorContext(c.ctx, "subscribe failed", "topic", t.Topic, "error", err)
		}
	}

	return nil
}

func (c *BridgeClient) subscribe(cl pahomqtt.Client, topic string, qos byte) error {
	handler := func(_ pahomqtt.Client, msg pahomqtt.Message) {
		start := time.Now()
		c.msgCount.Add(1)
		c.metrics.IncMessages(c.spec.Name, c.spec.Namespace, msg.Topic())
		c.msgHandler(msg.Topic(), msg.Payload())
		c.metrics.ObserveMessageLatency(c.spec.Name, c.spec.Namespace, msg.Topic(),
			time.Since(start).Seconds())
	}

	token := cl.Subscribe(topic, qos, handler)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("subscribe timeout: topic=%s", topic)
	}
	return token.Error()
}

func (c *BridgeClient) onConnected() {
	now := time.Now()
	c.mu.Lock()
	c.state = bridge.StateConnected
	c.lastConnected = &now
	c.errorMessage = ""
	c.mu.Unlock()

	c.metrics.SetConnected(c.spec.Name, c.spec.Namespace, c.spec.Host, true)
	c.log.InfoContext(c.ctx, "connected to MQTT broker", "host", c.spec.Host, "port", c.spec.Port)
	c.notifyStateChange()
}

func (c *BridgeClient) onConnectionLost(err error) {
	now := time.Now()
	c.mu.Lock()
	c.state = bridge.StateDisconnected
	c.lastDisconnected = &now
	if err != nil {
		c.errorMessage = err.Error()
	}
	c.mu.Unlock()

	c.metrics.SetConnected(c.spec.Name, c.spec.Namespace, c.spec.Host, false)
	c.log.WarnContext(c.ctx, "connection lost", "error", err)
	c.notifyStateChange()

	// Signal the run loop to reconnect (non-blocking).
	select {
	case c.reconnectCh <- struct{}{}:
	default:
	}
}

func (c *BridgeClient) setError(err error) {
	c.mu.Lock()
	c.state = bridge.StateError
	if err != nil {
		c.errorMessage = err.Error()
	}
	c.mu.Unlock()

	c.metrics.SetConnected(c.spec.Name, c.spec.Namespace, c.spec.Host, false)
	c.notifyStateChange()
}

func (c *BridgeClient) notifyStateChange() {
	if c.onStateChange != nil {
		c.onStateChange(c.spec.Name, c.spec.Namespace, c.Stats())
	}
}
