package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

const localClusterID = "local"

type Backend interface {
	Ready(context.Context) error
	Clusters() []kubernetes.Cluster
	ListNamespaces(context.Context) ([]kubernetes.Namespace, error)
	GetResource(context.Context, string, string, string) (kubernetes.ResourceDetail, error)
	ListFindings(context.Context, finding.Query) (finding.Page, error)
	GetFinding(context.Context, string) (finding.Finding, error)
	SummarizeFindings(context.Context, finding.Filter) (finding.Summary, error)
}

type Server struct {
	logger       *slog.Logger
	backend      Backend
	readyTimeout time.Duration
}

// NewHandler preserves the Phase 1.3 runtime behavior while Phase 1.4.1 establishes the
// provider-neutral identity contract. Authentication enforcement is activated by passing an
// Authenticator to NewHandlerWithOptions; no insecure built-in credential mechanism is assumed.
func NewHandler(logger *slog.Logger, backend Backend, readyTimeout time.Duration) http.Handler {
	return NewHandlerWithOptions(logger, backend, readyTimeout, HandlerOptions{})
}

// NewHandlerWithOptions builds the HTTP pipeline with request metadata first, then optional AuthN,
// then the existing read-only API routes. Request/correlation IDs therefore exist on AuthN errors.
func NewHandlerWithOptions(logger *slog.Logger, backend Backend, readyTimeout time.Duration, options HandlerOptions) http.Handler {
	server := &Server{
		logger:       logger,
		backend:      backend,
		readyTimeout: readyTimeout,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.healthz)
	mux.HandleFunc("GET /readyz", server.readyz)
	mux.HandleFunc("GET /api/v1/clusters", server.clusters)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces", server.namespaces)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}", server.resource)
	mux.HandleFunc("GET /api/v1/findings", server.findings)
	mux.HandleFunc("GET /api/v1/findings/summary", server.findingSummary)
	mux.HandleFunc("GET /api/v1/findings/{id}", server.findingDetail)

	var handler http.Handler = mux
	if options.Authenticator != nil {
		handler = server.authenticationMiddleware(options.Authenticator, handler)
	}
	return requestMetadataMiddleware(handler)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.readyTimeout)
	defer cancel()

	if err := s.backend.Ready(ctx); err != nil {
		s.logger.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) clusters(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.backend.Clusters()})
}

func (s *Server) namespaces(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("cluster") != localClusterID {
		writeError(w, http.StatusNotFound, "CLUSTER_NOT_FOUND", "cluster not found")
		return
	}

	items, err := s.backend.ListNamespaces(r.Context())
	if err != nil {
		s.logger.Warn("list namespaces failed", "error", err)
		writeKubernetesError(w, err, "NAMESPACE_LIST_FAILED", "unable to list namespaces")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) resource(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("cluster") != localClusterID {
		writeError(w, http.StatusNotFound, "CLUSTER_NOT_FOUND", "cluster not found")
		return
	}

	kind := strings.ToLower(r.PathValue("kind"))
	if kind != "pod" && kind != "pods" && kind != "deployment" && kind != "deployments" {
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_RESOURCE_KIND", "resource kind is not supported")
		return
	}

	resource, err := s.backend.GetResource(
		r.Context(),
		kind,
		r.PathValue("namespace"),
		r.PathValue("name"),
	)
	if err != nil {
		s.logger.Warn("get resource failed", "kind", kind, "error", err)
		writeKubernetesError(w, err, "RESOURCE_READ_FAILED", "unable to read resource")
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) findings(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseFindingFilter(w, r)
	if !ok {
		return
	}

	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be an integer between 1 and 200")
			return
		}
		limit = value
	}

	page, err := s.backend.ListFindings(r.Context(), finding.Query{
		Filter:   filter,
		Limit:    limit,
		Continue: r.URL.Query().Get("continue"),
	})
	if err != nil {
		s.writeFindingBackendError(w, err, "FINDING_LIST_FAILED", "unable to list findings")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) findingSummary(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseFindingFilter(w, r)
	if !ok {
		return
	}

	summary, err := s.backend.SummarizeFindings(r.Context(), filter)
	if err != nil {
		s.writeFindingBackendError(w, err, "FINDING_SUMMARY_FAILED", "unable to summarize findings")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) findingDetail(w http.ResponseWriter, r *http.Request) {
	item, err := s.backend.GetFinding(r.Context(), r.PathValue("id"))
	if err != nil {
		var apiErr *kubernetes.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			writeError(w, http.StatusNotFound, "FINDING_NOT_FOUND", "finding not found")
			return
		}
		s.logger.Warn("get finding failed", "error", err)
		writeError(w, http.StatusBadGateway, "FINDING_READ_FAILED", "unable to read finding")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func parseFindingFilter(w http.ResponseWriter, r *http.Request) (finding.Filter, bool) {
	query := r.URL.Query()
	cluster := strings.TrimSpace(query.Get("cluster"))
	if cluster == "" {
		cluster = localClusterID
	}
	if cluster != localClusterID {
		writeError(w, http.StatusNotFound, "CLUSTER_NOT_FOUND", "cluster not found")
		return finding.Filter{}, false
	}

	return finding.Filter{
		Cluster:   cluster,
		Namespace: query.Get("namespace"),
		Kind:      query.Get("kind"),
		Severity:  query.Get("severity"),
		Problem:   query.Get("problem"),
	}, true
}

func (s *Server) writeFindingBackendError(w http.ResponseWriter, err error, code, message string) {
	switch {
	case errors.Is(err, finding.ErrInvalidLimit):
		writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 200")
	case errors.Is(err, finding.ErrInvalidCursor):
		writeError(w, http.StatusBadRequest, "INVALID_CONTINUE_TOKEN", "continue token is invalid")
	case errors.Is(err, finding.ErrTooMany):
		writeError(w, http.StatusServiceUnavailable, "FINDING_SET_TOO_LARGE", "finding set exceeds the safe scan limit")
	default:
		s.logger.Warn("finding backend request failed", "error", err)
		writeError(w, http.StatusBadGateway, code, message)
	}
}

func writeKubernetesError(w http.ResponseWriter, err error, code, message string) {
	var apiErr *kubernetes.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "resource not found")
		return
	}
	writeError(w, http.StatusBadGateway, code, message)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
