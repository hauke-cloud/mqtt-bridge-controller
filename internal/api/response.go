package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
)

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type collectionResponse[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status, code := mapError(err)
	writeJSON(w, status, errorResponse{Error: err.Error(), Code: code})
}

func mapError(err error) (int, string) {
	switch {
	case errors.Is(err, bridge.ErrBridgeNotFound):
		return http.StatusNotFound, "BRIDGE_NOT_FOUND"
	case errors.Is(err, bridge.ErrBridgeExists):
		return http.StatusConflict, "BRIDGE_EXISTS"
	case errors.Is(err, bridge.ErrNotConnected):
		return http.StatusServiceUnavailable, "BRIDGE_NOT_CONNECTED"
	case errors.Is(err, bridge.ErrInvalidConfig):
		return http.StatusUnprocessableEntity, "INVALID_CONFIG"
	case errors.Is(err, bridge.ErrCredentialLookup):
		return http.StatusInternalServerError, "CREDENTIAL_LOOKUP_FAILED"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}
