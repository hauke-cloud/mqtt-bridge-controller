package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TLSConfig holds paths to server TLS material and optional mTLS CA.
type TLSConfig struct {
	CertFile string
	KeyFile  string
	// CAFile is the PEM-encoded CA certificate used to verify client certs.
	// When set, mTLS (RequireAndVerifyClientCert) is enforced on all routes
	// except /healthz and /readyz.
	CAFile string
}

// ServerConfig holds configuration for the HTTP/HTTPS API server.
type ServerConfig struct {
	Addr    string
	TLS     *TLSConfig
	Metrics prometheus.Gatherer
}

// Server is the REST API server.
type Server struct {
	srv *http.Server
	log *slog.Logger
}

// NewServer builds a chi router, wires all routes, and returns a ready Server.
func NewServer(
	cfg ServerConfig,
	log *slog.Logger,
	k8sClient client.Client,
	mgr BridgeStatsProvider,
) (*Server, error) {
	h := NewHandler(log, k8sClient, mgr)
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(newSlogMiddleware(log))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Unauthenticated probes — must NOT be behind mTLS middleware.
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(mgr))

	// Prometheus metrics endpoint — unauthenticated by default; place behind
	// mTLS in production by moving it into the secured group.
	if cfg.Metrics != nil {
		r.Handle("/metrics", promhttp.HandlerFor(cfg.Metrics, promhttp.HandlerOpts{}))
	}

	// mTLS-protected API routes.
	r.Group(func(r chi.Router) {
		if cfg.TLS != nil && cfg.TLS.CAFile != "" {
			r.Use(requireClientCert)
		}
		r.Route("/api/v1", func(r chi.Router) {
			r.Get("/bridges", h.ListBridges)
			r.Get("/bridges/{namespace}/{name}", h.GetBridge)
			r.Get("/bridges/{namespace}/{name}/status", h.GetBridgeStatus)
		})
	})

	httpSrv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if cfg.TLS != nil {
		tlsCfg, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("build TLS config: %w", err)
		}
		httpSrv.TLSConfig = tlsCfg
	}

	return &Server{srv: httpSrv, log: log.With("component", "api-server")}, nil
}

// Start begins serving. It blocks until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	s.log.InfoContext(ctx, "API server starting", "addr", s.srv.Addr)

	errCh := make(chan error, 1)
	go func() {
		var err error
		if s.srv.TLSConfig != nil {
			// Certificate and key already loaded into TLSConfig.
			err = s.srv.ListenAndServeTLS("", "")
		} else {
			err = s.srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

func buildTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %s: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", cfg.CAFile)
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func handleReadyz(mgr BridgeStatsProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := mgr.AllStats()
		for _, s := range stats {
			if s.State == "error" {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("degraded"))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
