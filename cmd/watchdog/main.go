package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/josephtknight/k8s-amt-node-watchdog/internal/amt"
	"github.com/josephtknight/k8s-amt-node-watchdog/internal/config"
	"github.com/josephtknight/k8s-amt-node-watchdog/internal/metrics"
	"github.com/josephtknight/k8s-amt-node-watchdog/internal/monitor"
	"github.com/josephtknight/k8s-amt-node-watchdog/internal/restarter"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if cfg.DryRun {
		slog.Info("running in DRY RUN mode — no actual power cycles will be performed")
	}

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		slog.Error("failed to get in-cluster config", "error", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		slog.Error("failed to create kubernetes client", "error", err)
		os.Exit(1)
	}

	// HTTP server for metrics and health probes (runs on all replicas)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	server := &http.Server{Addr: cfg.MetricsAddr, Handler: mux}
	go func() {
		slog.Info("starting HTTP server", "addr", cfg.MetricsAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Set up components
	amtClient := amt.NewClient(cfg.AMTUsername, cfg.AMTPassword, cfg.AMTPort)
	rst := restarter.New(cfg, clientset, amtClient)
	mon := monitor.New(cfg, clientset, rst)

	// Leader election
	hostname, _ := os.Hostname()
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaseName,
			Namespace: cfg.LeaseNamespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: hostname,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		server.Shutdown(shutdownCtx)
	}()

	slog.Info("starting leader election", "identity", hostname)

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   30 * time.Second,
		RenewDeadline:   20 * time.Second,
		RetryPeriod:     5 * time.Second,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				slog.Info("became leader, starting monitor")
				metrics.Leader.Set(1)
				mon.Run(ctx)
			},
			OnStoppedLeading: func() {
				slog.Info("lost leadership")
				metrics.Leader.Set(0)
			},
			OnNewLeader: func(identity string) {
				if identity != hostname {
					slog.Info("new leader elected", "leader", identity)
				}
			},
		},
	})
}
