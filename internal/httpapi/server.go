package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

const localClusterID = "local"

type Backend interface {
	Ready(context.Context) error
	Clusters() []kubernetes.Cluster
	ListNamespaces(context.Context) ([]kubernetes.Namespace, error)
	GetResource(context.Context, string, string, string) (kubernetes.ResourceDetail, error)
}

type Server struct {
	logger       *slog.Logger
	backend      Backend
	readyTimeout time.Duration
}

func NewHandler(logger *slog.Logger, backend Backend, readyTimeout time.Duration) http.Handler {
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
	return mux
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
