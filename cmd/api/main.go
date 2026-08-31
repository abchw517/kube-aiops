package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/abchw517/kube-aiops/internal/config"
	"github.com/abchw517/kube-aiops/internal/httpapi"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	readiness := kubernetes.NewReadinessChecker(kubernetes.ReadinessConfig{
		APIURL:    cfg.KubernetesAPIURL,
		TokenFile: cfg.KubernetesTokenFile,
		CAFile:    cfg.KubernetesCAFile,
		K8sGPTNS:  cfg.K8sGPTNamespace,
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewHandler(logger, readiness, cfg.ReadyTimeout),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("kube-aiops-api started", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		logger.Error("http server failed", "error", err)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("kube-aiops-api stopped")
}
