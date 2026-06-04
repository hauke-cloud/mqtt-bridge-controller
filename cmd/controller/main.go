package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/hauke-cloud/mqtt-bridge-controller/api/v1alpha1"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/api"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/bridge"
	k8sctrl "github.com/hauke-cloud/mqtt-bridge-controller/internal/k8s"
	"github.com/hauke-cloud/mqtt-bridge-controller/internal/metrics"
	mqttmgr "github.com/hauke-cloud/mqtt-bridge-controller/internal/mqtt"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		metricsAddr = flag.String("metrics-addr", ":8080", "controller-runtime metrics bind address")
		probeAddr   = flag.String("probe-addr", ":8081", "health probe bind address")
		apiAddr     = flag.String("api-addr", ":8443", "REST API bind address")
		tlsCertFile = flag.String("tls-cert", "/tls/tls.crt", "server TLS certificate")
		tlsKeyFile  = flag.String("tls-key", "/tls/tls.key", "server TLS private key")
		tlsCAFile   = flag.String("tls-ca", "/tls/ca.crt", "CA cert for mTLS client verification; empty string disables mTLS")
		logLevel    = flag.String("log-level", "info", "log level: debug, info, warn, error")
		leaderElect = flag.Bool("leader-elect", false, "enable leader election for HA deployments")
	)
	flag.Parse()

	log := newLogger(*logLevel)
	ctrl.SetLogger(zap.New(zap.UseDevMode(*logLevel == "debug")))

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	col := metrics.NewCollector(reg)

	mqttManager := mqttmgr.NewManager(
		log,
		col,
		func(topic string, payload []byte) {
			log.Debug("mqtt message received", "topic", topic, "bytes", len(payload))
		},
		nil, // state-change callback set below after k8s manager is ready
	)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: *metricsAddr},
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *leaderElect,
		LeaderElectionID:       "mqtt-bridge-controller.iot.hauke.cloud",
	})
	if err != nil {
		return fmt.Errorf("new manager: %w", err)
	}

	reconciler := k8sctrl.NewMQTTBridgeReconciler(
		mgr.GetClient(),
		log,
		mgr.GetEventRecorderFor("mqtt-bridge-controller"),
		mqttManager,
	)
	if err := reconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup reconciler: %w", err)
	}

	// Wire the state-change callback now that the reconciler exists.
	mqttManager.SetStateChangeCallback(func(name, namespace string, stats bridge.BridgeStats) {
		log.Debug("bridge state changed", "bridge", name, "namespace", namespace, "state", stats.State)
	})

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("add healthz: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("add readyz: %w", err)
	}

	var tlsCfg *api.TLSConfig
	if *tlsCertFile != "" && *tlsKeyFile != "" {
		tlsCfg = &api.TLSConfig{
			CertFile: *tlsCertFile,
			KeyFile:  *tlsKeyFile,
			CAFile:   *tlsCAFile,
		}
	}

	apiServer, err := api.NewServer(api.ServerConfig{
		Addr:    *apiAddr,
		TLS:     tlsCfg,
		Metrics: reg,
	}, log, mgr.GetClient(), mqttManager)
	if err != nil {
		return fmt.Errorf("build API server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)

	go func() {
		if err := apiServer.Start(ctx); err != nil {
			errCh <- fmt.Errorf("API server: %w", err)
		}
	}()

	go func() {
		if err := mgr.Start(ctx); err != nil {
			errCh <- fmt.Errorf("controller-runtime manager: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.InfoContext(ctx, "shutdown signal received, draining")
		mqttManager.StopAll()
		return nil
	case err := <-errCh:
		mqttManager.StopAll()
		return err
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
