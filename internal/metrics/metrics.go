package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Collector struct {
	connectionState *prometheus.GaugeVec
	messagesTotal   *prometheus.CounterVec
	reconnectTotal  *prometheus.CounterVec
	messageLatency  *prometheus.HistogramVec
	activeBridges   prometheus.Gauge
}

func NewCollector(reg prometheus.Registerer) *Collector {
	factory := promauto.With(reg)
	return &Collector{
		connectionState: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "mqttbridge",
			Name:      "connection_state",
			Help:      "Current connection state of an MQTT bridge (1=connected, 0=disconnected).",
		}, []string{"bridge", "namespace", "host"}),

		messagesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "mqttbridge",
			Name:      "messages_received_total",
			Help:      "Total MQTT messages received per bridge and topic.",
		}, []string{"bridge", "namespace", "topic"}),

		reconnectTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "mqttbridge",
			Name:      "reconnect_total",
			Help:      "Total reconnect attempts per bridge.",
		}, []string{"bridge", "namespace"}),

		messageLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "mqttbridge",
			Name:      "message_processing_seconds",
			Help:      "Time spent processing a single MQTT message.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"bridge", "namespace", "topic"}),

		activeBridges: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: "mqttbridge",
			Name:      "active_bridges",
			Help:      "Number of MQTTBridge CRs currently managed.",
		}),
	}
}

func (c *Collector) SetConnected(bridge, namespace, host string, connected bool) {
	v := 0.0
	if connected {
		v = 1.0
	}
	c.connectionState.WithLabelValues(bridge, namespace, host).Set(v)
}

func (c *Collector) DeleteBridge(bridge, namespace, host string) {
	c.connectionState.DeleteLabelValues(bridge, namespace, host)
	c.activeBridges.Dec()
}

func (c *Collector) IncMessages(bridge, namespace, topic string) {
	c.messagesTotal.WithLabelValues(bridge, namespace, topic).Inc()
}

func (c *Collector) IncReconnect(bridge, namespace string) {
	c.reconnectTotal.WithLabelValues(bridge, namespace).Inc()
}

func (c *Collector) ObserveMessageLatency(bridge, namespace, topic string, seconds float64) {
	c.messageLatency.WithLabelValues(bridge, namespace, topic).Observe(seconds)
}

func (c *Collector) IncActiveBridges() {
	c.activeBridges.Inc()
}
