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

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
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
	logger                  *slog.Logger
	backend                 Backend
	readyTimeout            time.Duration
	authorizer              authorization.Authorizer
	authorizationEnabled    bool
	findingDetailCapability authorization.Capability
	responseSanitizer       sanitizer.Sanitizer
}

// NewHandler preserves the existing provider-neutral runtime until concrete trusted AuthN/AuthZ
// adapters are configured. NewHandlerWithOptions activates the Phase 1.4 security pipeline.
func NewHandler(logger *slog.Logger, backend Backend, readyTimeout time.Duration) http.Handler {
	return NewHandlerWithOptions(logger, backend, readyTimeout, HandlerOptions{})
}

// NewHandlerWithOptions builds request metadata -> optional Audit -> optional AuthN -> route-level
// AuthZ -> read-only handlers -> typed Sanitizer -> JSON response. Request/correlation IDs therefore
// exist on every audited security outcome, while Audit can observe 401/403/5xx without changing the
// AuthN/AuthZ decision. A nil Sanitizer never disables response sanitization.
func NewHandlerWithOptions(logger *slog.Logger, backend Backend, readyTimeout time.Duration, options HandlerOptions) http.Handler {
	responseSanitizer := options.Sanitizer
	if responseSanitizer == nil {
		responseSanitizer = sanitizer.Default()
	}
	server := &Server{
		logger:               logger,
		backend:              backend,
		readyTimeout:         readyTimeout,
		authorizer:           options.Authorizer,
		authorizationEnabled: options.Authenticator != nil || options.Authorizer != nil,
		responseSanitizer:    responseSanitizer,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.healthz)
	mux.HandleFunc("GET /readyz", server.readyz)
	mux.HandleFunc("GET /api/v1/clusters", server.protectRoute("GET /api/v1/clusters", server.clusters))
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces", server.protectRoute("GET /api/v1/clusters/{cluster}/namespaces", server.namespaces))
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}", server.protectRoute("GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}", server.resource))
	mux.HandleFunc("GET /api/v1/findings", server.protectRoute("GET /api/v1/findings", server.findings))
	mux.HandleFunc("GET /api/v1/findings/summary", server.protectRoute("GET /api/v1/findings/summary", server.findingSummary))
	mux.HandleFunc("GET /api/v1/findings/{id}", server.protectRoute("GET /api/v1/findings/{id}", server.findingDetail))

	var handler http.Handler = mux
	if options.Authenticator != nil {
		handler = server.authenticationMiddleware(options.Authenticator, handler)
	}
	if options.AuditSink != nil {
		handler = server.auditMiddleware(mux, options.AuditSink, handler)
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
	safeResource, err := s.responseSanitizer.Resource(resource)
	if err != nil {
		s.writeSanitizationFailure(w, r, authorization.CapabilityResourcesRead)
		return
	}
	writeJSON(w, http.StatusOK, safeResource)
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
		Continue: strings.TrimSpace(r.URL.Query().Get("continue")),
	})
	if err != nil {
		s.writeFindingBackendError(w, err, "FINDING_LIST_FAILED", "unable to list findings")
		return
	}
	safePage, err := s.responseSanitizer.FindingPage(page)
	if err != nil {
		s.writeSanitizationFailure(w, r, authorization.CapabilityFindingsList)
		return
	}
	writeJSON(w, http.StatusOK, safePage)
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
	safeSummary, err := s.responseSanitizer.FindingSummary(summary)
	if err != nil {
		s.writeSanitizationFailure(w, r, authorization.CapabilityFindingsSummary)
		return
	}
	writeJSON(w, http.StatusOK, safeSummary)
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

	if s.authorizationEnabled {
		scope := authorization.ClusterScope(item.Cluster)
		if strings.TrimSpace(item.Namespace) != "" {
			scope = authorization.NamespaceScope(item.Cluster, item.Namespace)
		}
		if !s.authorizeRequest(w, r, s.authorizer, s.findingDetailCapability, scope) {
			return
		}
	}
	safeItem, err := s.responseSanitizer.Finding(item)
	if err != nil {
		s.writeSanitizationFailure(w, r, authorization.CapabilityFindingsRead)
		return
	}
	writeJSON(w, http.StatusOK, safeItem)
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
		Namespace: strings.TrimSpace(query.Get("namespace")),
		Kind:      strings.TrimSpace(query.Get("kind")),
		Severity:  strings.TrimSpace(query.Get("severity")),
		Problem:   strings.TrimSpace(query.Get("problem")),
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

func (s *Server) writeSanitizationFailure(w http.ResponseWriter, r *http.Request, capability authorization.Capability) {
	metadata := requestMetadataFromContext(r.Context())
	s.logger.Warn(
		"response sanitization blocked",
		"reason", "sanitizer_blocked",
		"request_id", metadata.RequestID,
		"correlation_id", metadata.CorrelationID,
		"capability", capability,
	)
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusBadGateway, "RESPONSE_SANITIZATION_FAILED", "response could not be emitted safely")
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
