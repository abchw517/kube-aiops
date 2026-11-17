package portal

import (
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
	"net/http"
)

func wrapAdmin(a *permissions.Admin, wikis *wiki.Service, s *identity.Sessions, next http.Handler, review *diagnosis.Review, managed *onboarding) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/admin/{resource}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, err := s.Authenticate(r.Context(), r)
		if err != nil {
			http.Error(w, "authentication required", 401)
			return
		}
		resource := r.PathValue("resource")
		if managed != nil && (resource == environmentKind || resource == businessKind) {
			capability := authorization.CapabilityEnvironmentsManage
			if resource == businessKind {
				capability = authorization.CapabilityBusinessLinesManage
			}
			allowed, err := catalogAccess(r, a, p, capability)
			if err != nil {
				http.Error(w, "authorization unavailable", http.StatusServiceUnavailable)
				return
			}
			if !allowed || (r.Method != http.MethodGet && !sameAdminOrigin(r, a.Origin())) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			managed.catalog(w, r, resource)
			return
		}
		if resource == "status" && r.Method == http.MethodGet {
			status := map[string]bool{"admin": p.Provider == permissions.AdminProvider, "findingAI": review.Enabled() && p.Provider == permissions.AdminProvider, "mustChangePassword": p.Provider == permissions.InitialAdminProvider}
			if managed != nil {
				status["manageEnvironments"], _ = catalogAccess(r, a, p, authorization.CapabilityEnvironmentsManage)
				status["manageBusinessLines"], _ = catalogAccess(r, a, p, authorization.CapabilityBusinessLinesManage)
			}
			adminhttp.WriteJSON(w, status)
			return
		}
		if p.Provider != permissions.AdminProvider && !(p.Provider == permissions.InitialAdminProvider && resource == "password") {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method != http.MethodGet && !sameAdminOrigin(r, a.Origin()) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if resource == "cluster-onboarding" && managed != nil {
			managed.admin(w, r)
			return
		}
		if resource == "finding-analysis" {
			instance := r.URL.Query().Get("cluster")
			selected := review
			if instance != "" && instance != "local" {
				selected = nil
				if managed != nil {
					selected = managed.review(instance)
				}
			}
			if selected.Enabled() {
				selected.ServeHTTP(w, r.WithContext(wikis.WithFindingLookup(identity.WithPrincipal(r.Context(), p), p)))
				return
			}
			http.Error(w, "该集群实例尚未启用 AI 复核", 404)
			return
		}
		a.Handle(w, r, resource, s)
	})
	wikis.Register(mux, s, review.AskWiki)
	mux.Handle("/", next)
	return mux
}

func sameAdminOrigin(r *http.Request, expected string) bool {
	origins := r.Header.Values("Origin")
	return len(origins) == 1 && origins[0] == expected
}

func catalogAccess(r *http.Request, authorizer authorization.Authorizer, p identity.Principal, capability authorization.Capability) (bool, error) {
	decision, err := authorizer.Authorize(r.Context(), authorization.DecisionRequest{
		Principal: p, Capability: capability, Scope: authorization.GlobalScope(),
	})
	return decision.Allowed, err
}
