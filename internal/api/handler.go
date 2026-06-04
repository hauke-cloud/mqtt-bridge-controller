package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/hauke-cloud/mqtt-bridge-controller/api/v1alpha1"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
)

// BridgeStatsProvider is the subset of mqtt.Manager needed by API handlers.
// Defined here so handlers can be tested without a real MQTT connection.
type BridgeStatsProvider interface {
	Stats(name, namespace string) (bridge.BridgeStats, error)
	AllStats() map[types.NamespacedName]bridge.BridgeStats
}

// Handler holds dependencies for all API handlers.
type Handler struct {
	log     *slog.Logger
	client  client.Client
	manager BridgeStatsProvider
}

func NewHandler(log *slog.Logger, c client.Client, mgr BridgeStatsProvider) *Handler {
	return &Handler{
		log:     log.With("component", "api-handler"),
		client:  c,
		manager: mgr,
	}
}

// BridgeResponse is the JSON representation of a bridge returned by the API.
type BridgeResponse struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`

	Spec struct {
		Host       string `json:"host"`
		Port       int32  `json:"port"`
		BridgeName string `json:"bridgeName"`
		DeviceType string `json:"deviceType"`
		Topics     []struct {
			Topic string `json:"topic"`
			Type  string `json:"type"`
		} `json:"topics"`
	} `json:"spec"`

	Status struct {
		ConnectionState      string     `json:"connectionState"`
		LastConnectedTime    *time.Time `json:"lastConnectedTime,omitempty"`
		LastDisconnectedTime *time.Time `json:"lastDisconnectedTime,omitempty"`
		MessagesReceived     int64      `json:"messagesReceived"`
		ReconnectCount       int32      `json:"reconnectCount"`
		ErrorMessage         string     `json:"errorMessage,omitempty"`
	} `json:"status"`
}

// ListBridges handles GET /api/v1/bridges
func (h *Handler) ListBridges(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePagination(r)

	var list v1alpha1.MQTTBridgeList
	if err := h.client.List(r.Context(), &list); err != nil {
		writeError(w, err)
		return
	}

	responses := make([]BridgeResponse, 0, len(list.Items))
	for i := range list.Items {
		responses = append(responses, h.toBridgeResponse(&list.Items[i]))
	}

	total := len(responses)
	end := offset + limit
	if offset >= total {
		responses = []BridgeResponse{}
	} else {
		if end > total {
			end = total
		}
		responses = responses[offset:end]
	}

	writeJSON(w, http.StatusOK, collectionResponse[BridgeResponse]{
		Items:  responses,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// GetBridge handles GET /api/v1/bridges/{namespace}/{name}
func (h *Handler) GetBridge(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	namespace := chi.URLParam(r, "namespace")

	var br v1alpha1.MQTTBridge
	if err := h.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: namespace}, &br); err != nil {
		writeError(w, toBridgeError(err))
		return
	}

	writeJSON(w, http.StatusOK, h.toBridgeResponse(&br))
}

// GetBridgeStatus handles GET /api/v1/bridges/{namespace}/{name}/status
func (h *Handler) GetBridgeStatus(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	namespace := chi.URLParam(r, "namespace")

	stats, err := h.manager.Stats(name, namespace)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) toBridgeResponse(br *v1alpha1.MQTTBridge) BridgeResponse {
	resp := BridgeResponse{
		Name:      br.Name,
		Namespace: br.Namespace,
	}
	resp.Spec.Host = br.Spec.Host
	resp.Spec.Port = br.Spec.Port
	resp.Spec.BridgeName = br.Spec.BridgeName
	resp.Spec.DeviceType = string(br.Spec.DeviceType)

	for _, t := range br.Spec.Topics {
		resp.Spec.Topics = append(resp.Spec.Topics, struct {
			Topic string `json:"topic"`
			Type  string `json:"type"`
		}{Topic: t.Topic, Type: string(t.Type)})
	}

	resp.Status.ConnectionState = string(br.Status.ConnectionState)
	resp.Status.MessagesReceived = br.Status.MessagesReceived
	resp.Status.ReconnectCount = br.Status.ReconnectCount
	resp.Status.ErrorMessage = br.Status.ErrorMessage

	if br.Status.LastConnectedTime != nil {
		t := br.Status.LastConnectedTime.UTC()
		resp.Status.LastConnectedTime = &t
	}
	if br.Status.LastDisconnectedTime != nil {
		t := br.Status.LastDisconnectedTime.UTC()
		resp.Status.LastDisconnectedTime = &t
	}

	// Enrich with real-time stats from manager if available.
	if rt, err := h.manager.Stats(br.Name, br.Namespace); err == nil {
		resp.Status.ConnectionState = string(rt.State)
		resp.Status.MessagesReceived = rt.MessagesReceived
		resp.Status.ReconnectCount = rt.ReconnectCount
		resp.Status.ErrorMessage = rt.ErrorMessage
		resp.Status.LastConnectedTime = rt.LastConnectedTime
		resp.Status.LastDisconnectedTime = rt.LastDisconnectedTime
	}

	return resp
}

func toBridgeError(err error) error {
	if isNotFound(err) {
		return bridge.ErrBridgeNotFound
	}
	return err
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	// controller-runtime wraps apierrors.IsNotFound
	type notFound interface{ NotFound() bool }
	if nf, ok := err.(notFound); ok {
		return nf.NotFound()
	}
	return false
}
