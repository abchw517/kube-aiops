package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type ClusterTarget struct {
	Backend    Backend
	Correlator correlation.Correlator
}

// ClusterBackends retains the local backend for readiness and legacy calls.
type ClusterBackends struct {
	Backend
	Targets         map[string]ClusterTarget
	ManagedEvidence func(context.Context, correlation.CorrelationScope, correlation.Correlator) (correlation.Correlator, error)
	mu              sync.RWMutex
	managed         map[string]ClusterTarget
	local           *ClusterTarget
}

func (b *ClusterBackends) SetLocal(target ClusterTarget) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.local = &target
}

func (b *ClusterBackends) Local() (ClusterTarget, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.local != nil {
		return *b.local, b.local.Backend != nil
	}
	return ClusterTarget{Backend: b.Backend}, b.Backend != nil
}

func (b *ClusterBackends) SetManaged(id string, target ClusterTarget) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.managed == nil {
		b.managed = make(map[string]ClusterTarget)
	}
	if target.Backend == nil {
		delete(b.managed, id)
		return
	}
	b.managed[id] = target
}

func (b *ClusterBackends) Lookup(id string) (ClusterTarget, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if t, ok := b.managed[id]; ok {
		return t, true
	}
	t, ok := b.Targets[id]
	return t, ok
}

func queryCluster(r *http.Request) string {
	if id := strings.TrimSpace(r.URL.Query().Get("cluster")); id != "" {
		return id
	}
	return localClusterID
}

func (s *Server) target(w http.ResponseWriter, r *http.Request, id string) (ClusterTarget, bool) {
	if id == localClusterID {
		if registry, ok := s.backend.(*ClusterBackends); ok {
			if target, ok := registry.Local(); ok {
				if target.Correlator == nil {
					target.Correlator = s.correlator
				}
				return target, true
			}
			writeError(w, http.StatusNotFound, "CLUSTER_NOT_FOUND", "cluster not found")
			return ClusterTarget{}, false
		}
		return ClusterTarget{s.backend, s.correlator}, true
	}
	if registry, ok := s.backend.(*ClusterBackends); ok {
		if target, ok := registry.Lookup(id); ok {
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
	items := make([]kubernetes.Cluster, 0)
	if registry, ok := s.backend.(*ClusterBackends); ok {
		if local, ok := registry.Local(); ok {
			items = append(items, local.Backend.Clusters()...)
		}
		registry.mu.RLock()
		for _, targets := range []map[string]ClusterTarget{registry.Targets, registry.managed} {
			for _, target := range targets {
				items = append(items, target.Backend.Clusters()...)
			}
		}
		registry.mu.RUnlock()
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	} else {
		items = append(items, s.backend.Clusters()...)
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
