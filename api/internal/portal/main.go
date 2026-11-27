package portal

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
	"github.com/abchw517/kube-aiops/api/internal/holmes"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/config"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/security"
)

func Run() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if len(os.Args) > 1 {
		var run func() error
		switch {
		case len(os.Args) == 2 && os.Args[1] == "identity-import":
			run = permissions.Import
		case len(os.Args) == 2 && os.Args[1] == "admin-reset-password":
			run = permissions.ResetSuperPassword
		case len(os.Args) == 2 && os.Args[1] == "cluster-report":
			run = clusterReport
		default:
			logger.Error("usage: kube-aiops-api [identity-import|admin-reset-password|cluster-report]")
			os.Exit(2)
		}
		if err := run(); err != nil {
			logger.Error("command failed", "command", os.Args[1], "error", err)
			os.Exit(1)
		}
		logger.Info("command complete", "command", os.Args[1])
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

	queryBackend, err := loadClusters(os.Getenv("KUBERNETES_CLUSTERS_FILE"), backend, cfg)
	if err != nil {
		logger.Error("load cluster configuration failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bundle := defaultProductionBundle(logger)
	var wrap func(http.Handler) http.Handler
	var managed *onboarding
	writeTimeout := 10 * time.Second
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
		var engineAI func(context.Context) (kubernetes.EngineAIConfig, error)
		if os.Getenv("FINDING_AI_ENGINE_ENABLED") == "true" {
			approvedEndpoint := strings.TrimSpace(os.Getenv("FINDING_AI_ALLOWED_ENDPOINT"))
			engineAI = func(ctx context.Context) (kubernetes.EngineAIConfig, error) {
				return backend.EngineAI(ctx, approvedEndpoint)
			}
		}
		review, reviewErr := diagnosis.New(backend, store, engineAI)
		if reviewErr != nil {
			logger.Error("invalid finding AI configuration", "error", reviewErr)
			os.Exit(1)
		}
		if review != nil {
			review.SetCorrelator(correlator)
			if registry, ok := queryBackend.(*httpapi.ClusterBackends); ok {
				registry.Backend = review
			} else {
				queryBackend = review
			}
			if review.Enabled() {
				writeTimeout = diagnosis.Timeout + 5*time.Second
			}
		}
		if store != nil {
			registry, ok := queryBackend.(*httpapi.ClusterBackends)
			if !ok {
				registry = &httpapi.ClusterBackends{Backend: queryBackend}
				queryBackend = registry
			}
			writeTimeout = diagnosis.Timeout + 5*time.Second
			managed, err = newOnboarding(store, registry)
			if err != nil {
				logger.Error("load managed cluster directory failed", "error", err)
				os.Exit(1)
			}
		}
		wrap, err = configureIdentityHandler(ctx, &bundle, store, review, managed, correlator)
		if err != nil {
			logger.Error("identity configuration invalid", "reason", "identity_config_invalid", "error", err)
			os.Exit(1)
		}
	}
	handler, err := buildHandler(
		logger,
		queryBackend,
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
	if managed != nil {
		handler = managed.reporting(handler)
		if err := managed.initLocal(ctx, backend); err != nil {
			logger.Error("load local cluster configuration failed", "error", err)
			os.Exit(1)
		}
		if managed.runDiagnostics != nil {
			go managed.runDiagnostics(ctx)
		}
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for ctx.Err() == nil {
				if err := managed.pollCollections(ctx); err != nil && ctx.Err() == nil {
					logger.Warn("cluster collection failed", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}

	if managed != nil || os.Getenv("HOLMES_BINDINGS_FILE") != "" {
		writeTimeout = max(writeTimeout, 55*time.Second)
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), writeTimeout+5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("kube-aiops-api stopped")
}

func configureIdentityHandler(ctx context.Context, bundle *security.Bundle, store *identitystore.Store, review *diagnosis.Review, managed *onboarding, correlators ...correlation.Correlator) (func(http.Handler) http.Handler, error) {
	if store == nil {
		sessions, err := permissions.Configure(bundle)
		if sessions == nil {
			return nil, err
		}
		return sessions.Wrap, err
	}
	admin, err := permissions.New(context.Background(), store)
	if err != nil {
		return nil, err
	}
	sessions, err := identity.NewSessions(os.Getenv("AUTH_ORIGIN"), map[string]identity.PasswordVerifier{"local": admin.Local, "ldap": admin.Directory})
	if err != nil {
		return nil, err
	}
	sessions.SessionLifetimeMinutes = admin.SessionLifetimeMinutes
	sessions.ValidateSession, sessions.RecordLogin = admin.ValidateSession, admin.RecordLogin
	if managed != nil {
		admin.SetScopeResolvers(managed.clusterEnvironment, managed.namespaceBusiness)
	}
	go admin.RunDirectoryChecks(ctx)
	go admin.RunAuditRetention(ctx)
	bundle.Authenticator, bundle.Authorizer = sessions, admin
	wikis := wiki.New(store, admin, admin.Origin())
	workspace, err := holmes.OpenWorkspace(store.DB)
	if err != nil {
		return nil, err
	}

	if managed != nil {
		wikis.SetTargetResolver(managed.wikiTarget)
	}
	var localEvidence correlation.Correlator
	if len(correlators) > 0 {
		localEvidence = correlators[0]
	}
	incidents, err := configureIncidents(ctx, store, admin, sessions, managed, localEvidence)
	if err != nil {
		return nil, err
	}
	if incidents != nil {
		provider := func(i incident.Incident) *diagnosis.Review {
			if i.Scope.ClusterID == "local" {
				return review
			}
			return managed.review(i.Scope.ClusterID)
		}
		incidents.ModelAvailable = func(i incident.Incident) bool { return provider(i).Enabled() }
		incidents.Model = func(ctx context.Context, i incident.Incident, prompt string, data []byte) (string, error) {
			scope := holmes.Scope{Source: "incident", SourceID: i.Scope.ClusterID, Environment: i.Scope.EnvironmentID, Business: i.Scope.BusinessID, Cluster: i.Scope.ClusterID, Namespace: i.Scope.Namespace}
			if answer, err := holmes.Chat(holmes.WithWorkspace(ctx, workspace, nil), scope, prompt, json.RawMessage(data), holmes.AssessmentFormat()); err == nil {
				return answer, nil
			}
			return provider(i).AnalyzeIncident(ctx, prompt, data)
		}
		managed.runDiagnostics = incidents.RunBackground
		if managed.registry != nil {
			managed.registry.ManagedEvidence = func(ctx context.Context, query correlation.CorrelationScope, base correlation.Correlator) (correlation.Correlator, error) {
				scope, err := managed.resolveDiagnosticScope(ctx, incident.Scope{ClusterID: query.Cluster, Namespace: query.Namespace})
				if errors.Is(err, incident.ErrBadScope) {
					return base, nil
				}
				if err != nil {
					return nil, correlation.ErrUnavailable
				}
				service, err := applyObservability(ctx, incidents, scope, base)
				if err != nil {
					return nil, correlation.ErrUnavailable
				}
				return service, nil
			}
		}
	}
	go wikis.RunSchedule(ctx)
	return func(next http.Handler) http.Handler {
		handler := wrapAdmin(admin, wikis, sessions, next, review, managed, incidents)
		return sessions.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := holmes.WithWorkspace(r.Context(), workspace, func(ctx context.Context, id string, revision int, scope holmes.Scope) bool {
				actor, err := sessions.Authenticate(ctx, r)
				if err != nil {
					return false
				}
				_, err = wikis.SkillManual(ctx, actor, id, revision, scope.Cluster, scope.Namespace)
				return err == nil
			})
			handler.ServeHTTP(w, r.WithContext(ctx))
		}))
	}, nil
}
