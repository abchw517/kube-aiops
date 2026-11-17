package portal

import (
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
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
		if resource == "status" && r.Method == http.MethodGet {
			adminhttp.WriteJSON(w, map[string]bool{"admin": p.Provider == permissions.AdminProvider, "findingAI": review.Enabled() && p.Provider == permissions.AdminProvider, "mustChangePassword": p.Provider == permissions.InitialAdminProvider})
			return
		}
		if p.Provider != permissions.AdminProvider && !(p.Provider == permissions.InitialAdminProvider && resource == "password") {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method != http.MethodGet {
			origins := r.Header.Values("Origin")
			if len(origins) != 1 || origins[0] != a.Origin() {
				http.Error(w, "forbidden", 403)
				return
			}
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
