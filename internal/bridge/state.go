package bridge

import (
	"time"

	v1alpha1 "github.com/hauke-cloud/mqtt-bridge-controller/api/v1alpha1"
)

// ConnectionState mirrors v1alpha1.ConnectionState for internal use.
type ConnectionState = v1alpha1.ConnectionState

const (
	StateConnected    = v1alpha1.ConnectionStateConnected
	StateDisconnected = v1alpha1.ConnectionStateDisconnected
	StateConnecting   = v1alpha1.ConnectionStateConnecting
	StateError        = v1alpha1.ConnectionStateError
)

// BridgeSpec is the normalised configuration passed to the MQTT manager.
type BridgeSpec struct {
	Name      string
	Namespace string
	Host      string
	Port      int32
	Username  string
	Password  string
	ClientID  string
	Topics    []TopicSpec
	// MaxBackoff caps the exponential reconnect delay.
	MaxBackoff time.Duration
}

type TopicSpec struct {
	Topic string
	QoS   byte
}

// BridgeStats holds runtime counters for a bridge connection.
type BridgeStats struct {
	State                ConnectionState
	MessagesReceived     int64
	ReconnectCount       int32
	LastConnectedTime    *time.Time
	LastDisconnectedTime *time.Time
	ErrorMessage         string
}
