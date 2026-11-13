package httpapi

import (
	"net/http"
	"strings"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

type ClusterTarget struct {
	Backend    Backend
	Correlator correlation.Correlator
}

// ClusterBackends retains the local backend for readiness and legacy calls.
type ClusterBackends struct {
	Backend
	Targets map[string]ClusterTarget
}

func queryCluster(r *http.Request) string {
	if id := strings.TrimSpace(r.URL.Query().Get("cluster")); id != "" {
		return id
	}
	return localClusterID
}

func (s *Server) target(w http.ResponseWriter, r *http.Request, id string) (ClusterTarget, bool) {
	if id == localClusterID {
		return ClusterTarget{s.backend, s.correlator}, true
	}
	if registry, ok := s.backend.(*ClusterBackends); ok {
		if target, ok := registry.Targets[id]; ok {
			if s.authorizationEnabled && !s.authorizeRequest(w, r, s.authorizer, authorization.CapabilityClustersList, authorization.ClusterScope(id)) {
				return ClusterTarget{}, false
			}
			return target, true
		}
	}
	writeError(w, http.StatusNotFound, "CLUSTER_NOT_FOUND", "cluster not found")
	return ClusterTarget{}, false
}

func (s *Server) visibleClusters(w http.ResponseWriter, r *http.Request) ([]kubernetes.Cluster, bool) {
	items := append([]kubernetes.Cluster{}, s.backend.Clusters()...)
	if registry, ok := s.backend.(*ClusterBackends); ok {
		for _, target := range registry.Targets {
			items = append(items, target.Backend.Clusters()...)
		}
	}
	if _, multi := s.backend.(*ClusterBackends); !multi || !s.authorizationEnabled {
		return items, true
	}
	principal, _ := identity.PrincipalFromContext(r.Context())
	visible := items[:0]
	for _, item := range items {
		decision, err := s.authorizer.Authorize(r.Context(), authorization.DecisionRequest{
			Principal: principal, Capability: authorization.CapabilityClustersList, Scope: authorization.ClusterScope(item.ID),
		})
		if err != nil {
			writeAuthorizationUnavailable(w)
			return nil, false
		}
		if decision.Allowed {
			visible = append(visible, item)
		}
	}
	return visible, true
}
