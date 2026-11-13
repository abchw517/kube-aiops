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
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/identitystore"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/security"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "identity-import" {
			logger.Error("usage: kube-aiops-api [identity-import]")
			os.Exit(2)
		}
		if err := importIdentity(); err != nil {
			logger.Error("identity import failed", "error", err)
			os.Exit(1)
		}
		logger.Info("identity import complete")
		return
	}

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
	correlator, err := buildCorrelator(cfg, backend)
	if err != nil {
		logger.Error("build correlation service failed", "reason", "correlation_service_invalid", "error", err)
		os.Exit(1)
	}

	bundle := defaultProductionBundle(logger)
	var wrap func(http.Handler) http.Handler
	if cfg.SecurityMode == security.ModeProduction {
		var store *identitystore.Store
		if path := os.Getenv("AUTH_SQLITE_PATH"); path != "" {
			store, err = identitystore.Open(path)
			if err != nil {
				logger.Error("cannot open identity database", "reason", "identity_database_unavailable")
				os.Exit(1)
			}
			defer store.Close()
		}
		wrap, err = configureIdentityHandler(&bundle, store)
		if err != nil {
			logger.Error("identity configuration invalid", "reason", "identity_config_invalid", "error", err)
			os.Exit(1)
		}
	}
	handler, err := buildHandler(
		logger,
		backend,
		cfg.ReadyTimeout,
		cfg.SecurityMode,
		bundle,
		correlator,
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

	if wrap != nil {
		handler = wrap(handler)
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

func configureIdentityHandler(bundle *security.Bundle, store *identitystore.Store) (func(http.Handler) http.Handler, error) {
	if store == nil {
		sessions, err := configureIdentity(bundle)
		if sessions == nil {
			return nil, err
		}
		return sessions.Wrap, err
	}
	admin, err := newIdentityAdmin(context.Background(), store)
	if err != nil {
		return nil, err
	}
	sessions, err := identity.NewSessions(os.Getenv("AUTH_ORIGIN"), map[string]identity.PasswordVerifier{"local": admin.local, "ldap": admin.directory})
	if err != nil {
		return nil, err
	}
	bundle.Authenticator, bundle.Authorizer = sessions, admin
	return func(next http.Handler) http.Handler { return sessions.Wrap(admin.wrap(sessions, next)) }, nil
}
