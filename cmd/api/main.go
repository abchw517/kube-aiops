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
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/security"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	backend := kubernetes.NewClient(kubernetes.Config{
		APIURL:     cfg.KubernetesAPIURL,
		TokenFile:  cfg.KubernetesTokenFile,
		CAFile:     cfg.KubernetesCAFile,
		K8sGPTNS:   cfg.K8sGPTNamespace,
		K8sGPTName: cfg.K8sGPTName,
	})

	handler, err := buildHandler(
		logger,
		backend,
		cfg.ReadyTimeout,
		cfg.SecurityMode,
		defaultProductionBundle(logger),
	)
	if err != nil {
		component := "composition"
		var validationErr *security.ValidationError
		if errors.As(err, &validationErr) {
			component = validationErr.Component
		}
		logger.Error(
			"security composition invalid",
			"reason", "security_bundle_invalid",
			"component", component,
			"mode", cfg.SecurityMode,
		)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("kube-aiops-api started", "addr", cfg.HTTPAddr, "security_mode", cfg.SecurityMode)
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
