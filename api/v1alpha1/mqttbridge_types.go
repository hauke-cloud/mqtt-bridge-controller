package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=mb
// +kubebuilder:printcolumn:name="Bridge",type="string",JSONPath=".spec.bridgeName"
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".spec.host"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.connectionState"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MQTTBridge struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MQTTBridgeSpec   `json:"spec"`
	Status MQTTBridgeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type MQTTBridgeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MQTTBridge `json:"items"`
}

type MQTTBridgeSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=1883
	Port int32 `json:"port,omitempty"`

	// +kubebuilder:validation:Enum=tasmota;generic
	// +kubebuilder:default=tasmota
	DeviceType DeviceType `json:"deviceType,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	BridgeName string `json:"bridgeName"`

	CredentialsSecretRef *SecretKeyRef `json:"credentialsSecretRef,omitempty"`

	Topics []BridgeTopic `json:"topics,omitempty"`

	// +kubebuilder:default=false
	DiscoveryEnabled bool `json:"discoveryEnabled,omitempty"`

	// Reconnect backoff cap in seconds.
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=300
	// +kubebuilder:default=60
	MaxReconnectBackoffSeconds int32 `json:"maxReconnectBackoffSeconds,omitempty"`
}

// +kubebuilder:validation:Enum=tasmota;generic
type DeviceType string

const (
	DeviceTypeTasmota DeviceType = "tasmota"
	DeviceTypeGeneric DeviceType = "generic"
)

type SecretKeyRef struct {
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key in the Secret whose value is used as the MQTT username.
	// Defaults to "username" when empty.
	UsernameKey string `json:"usernameKey,omitempty"`

	// Key in the Secret whose value is used as the MQTT password.
	// Defaults to "password" when empty.
	PasswordKey string `json:"passwordKey,omitempty"`
}

type BridgeTopic struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Topic string `json:"topic"`

	// +kubebuilder:validation:Enum=telemetry;result;state;command
	Type TopicType `json:"type"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	// +kubebuilder:default=1
	QoS int32 `json:"qos,omitempty"`
}

// +kubebuilder:validation:Enum=telemetry;result;state;command
type TopicType string

const (
	TopicTypeTelemetry TopicType = "telemetry"
	TopicTypeResult    TopicType = "result"
	TopicTypeState     TopicType = "state"
	TopicTypeCommand   TopicType = "command"
)

type MQTTBridgeStatus struct {
	// +kubebuilder:validation:Enum=connected;disconnected;connecting;error
	ConnectionState ConnectionState `json:"connectionState,omitempty"`

	LastConnectedTime *metav1.Time `json:"lastConnectedTime,omitempty"`

	LastDisconnectedTime *metav1.Time `json:"lastDisconnectedTime,omitempty"`

	// Total MQTT messages received since last connect.
	MessagesReceived int64 `json:"messagesReceived,omitempty"`

	// Number of reconnect attempts since startup.
	ReconnectCount int32 `json:"reconnectCount,omitempty"`

	// Human-readable error message when connectionState=error.
	ErrorMessage string `json:"errorMessage,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:validation:Enum=connected;disconnected;connecting;error
type ConnectionState string

const (
	ConnectionStateConnected    ConnectionState = "connected"
	ConnectionStateDisconnected ConnectionState = "disconnected"
	ConnectionStateConnecting   ConnectionState = "connecting"
	ConnectionStateError        ConnectionState = "error"
)

const (
	ConditionTypeReady     = "Ready"
	ConditionTypeConnected = "Connected"
)
