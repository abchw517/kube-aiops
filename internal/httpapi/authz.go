package httpapi

import (
	"net/http"
	"strings"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
)

type scopeResolver func(*http.Request) authorization.Scope

type protectedRoute struct {
	pattern       string
	capability    authorization.Capability
	resolveScope  scopeResolver
	deferredScope bool
	handler       http.Handler
}

func (s *Server) protectedRoutes() []protectedRoute {
	return []protectedRoute{
		{
			pattern:      "GET /api/v1/clusters",
			capability:   authorization.CapabilityClustersList,
			resolveScope: func(*http.Request) authorization.Scope { return authorization.GlobalScope() },
			handler:      http.HandlerFunc(s.clusters),
		},
		{
			pattern:      "GET /api/v1/clusters/{cluster}/namespaces",
			capability:   authorization.CapabilityNamespacesList,
			resolveScope: clusterPathScope,
			handler:      http.HandlerFunc(s.namespaces),
		},
		{
			pattern:      "GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}",
			capability:   authorization.CapabilityResourcesRead,
			resolveScope: namespacePathScope,
			handler:      http.HandlerFunc(s.resource),
		},
		{
			pattern:      "GET /api/v1/findings",
			capability:   authorization.CapabilityFindingsList,
			resolveScope: findingQueryScope,
			handler:      http.HandlerFunc(s.findings),
		},
		{
			pattern:      "GET /api/v1/findings/summary",
			capability:   authorization.CapabilityFindingsSummary,
			resolveScope: findingQueryScope,
			handler:      http.HandlerFunc(s.findingSummary),
		},
		{
			pattern:       "GET /api/v1/findings/{id}",
			capability:    authorization.CapabilityFindingsRead,
			deferredScope: true,
			handler:       http.HandlerFunc(s.findingDetail),
		},
	}
}

func (s *Server) registerProtectedRoutes(mux *http.ServeMux, options HandlerOptions) {
	authorizationEnabled := options.Authenticator != nil || options.Authorizer != nil
	s.authorizer = options.Authorizer
	s.authorizationEnabled = authorizationEnabled

	for _, route := range s.protectedRoutes() {
		handler := route.handler
		if route.deferredScope {
			s.findingDetailCapability = route.capability
			if authorizationEnabled {
				handler = s.authorizationPrerequisiteMiddleware(options.Authorizer, route.capability, handler)
			}
		} else if authorizationEnabled {
			handler = s.authorizationMiddleware(options.Authorizer, route.capability, route.resolveScope, handler)
		}
		mux.Handle(route.pattern, handler)
	}
}

func (s *Server) authorizationPrerequisiteMiddleware(
	authorizer authorization.Authorizer,
	capability authorization.Capability,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := identity.PrincipalFromContext(r.Context()); !ok {
			writeAuthenticationRequired(w)
			return
		}
		if authorizer == nil {
			s.logAuthorizationUnavailable(r, capability, authorization.Scope{}, "authorizer_not_configured")
			writeAuthorizationUnavailable(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorizationMiddleware(
	authorizer authorization.Authorizer,
	capability authorization.Capability,
	resolveScope scopeResolver,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeRequest(w, r, authorizer, capability, resolveScope(r)) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorizeRequest(
	w http.ResponseWriter,
	r *http.Request,
	authorizer authorization.Authorizer,
	capability authorization.Capability,
	scope authorization.Scope,
) bool {
	principal, ok := identity.PrincipalFromContext(r.Context())
	if !ok {
		writeAuthenticationRequired(w)
		return false
	}
	if authorizer == nil {
		s.logAuthorizationUnavailable(r, capability, scope, "authorizer_not_configured")
		writeAuthorizationUnavailable(w)
		return false
	}

	decision, err := authorizer.Authorize(r.Context(), authorization.DecisionRequest{
		Principal:  principal,
		Capability: capability,
		Scope:      scope,
	})
	if err != nil {
		s.logAuthorizationUnavailable(r, capability, scope, "authorizer_error")
		writeAuthorizationUnavailable(w)
		return false
	}
	if !decision.Allowed {
		writeForbidden(w)
		return false
	}
	return true
}

func (s *Server) logAuthorizationUnavailable(
	r *http.Request,
	capability authorization.Capability,
	scope authorization.Scope,
	reason string,
) {
	metadata := requestMetadataFromContext(r.Context())
	s.logger.Warn(
		"authorization unavailable",
		"reason", reason,
		"capability", capability,
		"cluster", scope.Cluster,
		"namespace", scope.Namespace,
		"request_id", metadata.RequestID,
		"correlation_id", metadata.CorrelationID,
	)
}

func clusterPathScope(r *http.Request) authorization.Scope {
	return authorization.ClusterScope(r.PathValue("cluster"))
}

func namespacePathScope(r *http.Request) authorization.Scope {
	return authorization.NamespaceScope(r.PathValue("cluster"), r.PathValue("namespace"))
}

func findingQueryScope(r *http.Request) authorization.Scope {
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	if cluster == "" {
		cluster = localClusterID
	}
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if namespace == "" {
		return authorization.ClusterScope(cluster)
	}
	return authorization.NamespaceScope(cluster, namespace)
}

func writeAuthorizationUnavailable(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusServiceUnavailable, "AUTHORIZATION_UNAVAILABLE", "authorization service unavailable")
}
