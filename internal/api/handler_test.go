package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

// ─── stubs ───────────────────────────────────────────────────────────────────

type stubK8sClient struct {
	client.Client
	listFn func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
	getFn  func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
}

func (s *stubK8sClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if s.listFn != nil {
		return s.listFn(ctx, list, opts...)
	}
	return nil
}

func (s *stubK8sClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if s.getFn != nil {
		return s.getFn(ctx, key, obj, opts...)
	}
	return nil
}

type stubStatsProvider struct {
	stats    bridge.BridgeStats
	statsErr error
}

func (s *stubStatsProvider) Stats(_, _ string) (bridge.BridgeStats, error) {
	return s.stats, s.statsErr
}

func (s *stubStatsProvider) AllStats() map[types.NamespacedName]bridge.BridgeStats {
	return nil
}

// ─── error mapping ───────────────────────────────────────────────────────────

func TestWriteError_Mapping(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{bridge.ErrBridgeNotFound, http.StatusNotFound, "BRIDGE_NOT_FOUND"},
		{bridge.ErrBridgeExists, http.StatusConflict, "BRIDGE_EXISTS"},
		{bridge.ErrNotConnected, http.StatusServiceUnavailable, "BRIDGE_NOT_CONNECTED"},
		{bridge.ErrInvalidConfig, http.StatusUnprocessableEntity, "INVALID_CONFIG"},
		{errors.New("unknown"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.wantCode, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			writeError(w, tc.err)

			if w.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", w.Code, tc.wantStatus)
			}
			var body map[string]string
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body["code"] != tc.wantCode {
				t.Errorf("code: got %s, want %s", body["code"], tc.wantCode)
			}
		})
	}
}

// ─── pagination ──────────────────────────────────────────────────────────────

func TestParsePagination_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	limit, offset := parsePagination(req)
	if limit != 20 {
		t.Errorf("default limit: want 20, got %d", limit)
	}
	if offset != 0 {
		t.Errorf("default offset: want 0, got %d", offset)
	}
}

func TestParsePagination_Custom(t *testing.T) {
	cases := []struct {
		query      string
		wantLimit  int
		wantOffset int
	}{
		{"limit=50&offset=10", 50, 10},
		{"limit=200", 100, 0}, // capped at max
		{"limit=0", 1, 0},     // min 1
		{"limit=abc", 20, 0},  // invalid → default
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			limit, offset := parsePagination(req)
			if limit != tc.wantLimit {
				t.Errorf("limit: got %d, want %d", limit, tc.wantLimit)
			}
			if offset != tc.wantOffset {
				t.Errorf("offset: got %d, want %d", offset, tc.wantOffset)
			}
		})
	}
}

// ─── handlers ────────────────────────────────────────────────────────────────

func TestListBridges_Empty(t *testing.T) {
	h := NewHandler(testLog(), &stubK8sClient{}, &stubStatsProvider{statsErr: bridge.ErrBridgeNotFound})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/bridges", nil)
	h.ListBridges(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp collectionResponse[BridgeResponse]
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("total: got %d, want 0", resp.Total)
	}
}

func TestGetBridge_NotFound(t *testing.T) {
	h := NewHandler(testLog(), &stubK8sClient{
		getFn: func(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return bridge.ErrBridgeNotFound
		},
	}, &stubStatsProvider{})

	r := chi.NewRouter()
	r.Get("/api/v1/bridges/{namespace}/{name}", h.GetBridge)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/bridges/default/nonexistent", nil)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestGetBridgeStatus_NotFound(t *testing.T) {
	h := NewHandler(testLog(), &stubK8sClient{}, &stubStatsProvider{
		statsErr: bridge.ErrBridgeNotFound,
	})

	r := chi.NewRouter()
	r.Get("/api/v1/bridges/{namespace}/{name}/status", h.GetBridgeStatus)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/bridges/default/gone/status", nil)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}
