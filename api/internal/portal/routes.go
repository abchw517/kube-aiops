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
		admin := permissions.IsAdministrator(p)
		if resource == "status" && r.Method == http.MethodGet {
			status := map[string]bool{"admin": admin, "findingAI": review.Enabled() && admin, "mustChangePassword": permissions.MustChangePassword(p), "canChangePassword": permissions.CanChangePassword(p), "superAdministrator": p.Subject == "local:admin" && p.Provider == permissions.AdminProvider}
			if managed != nil {
				status["manageEnvironments"] = status["admin"]
				status["manageBusinessLines"] = status["admin"]
			}
			adminhttp.WriteJSON(w, status)
			return
		}
		if (resource == "password" && !permissions.CanChangePassword(p)) || (resource != "password" && !admin) {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method != http.MethodGet && !sameAdminOrigin(r, a.Origin()) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if (resource == "users" || resource == "roles" || resource == "policy") && p.Subject != "local:admin" {
			http.Error(w, "forbidden", 403)
			return
		}
		if managed != nil {
			switch resource {
			case environmentKind, businessKind:
				a.AuditChange(w, r, p.Subject, "维护资源目录", "中", resource, func(w http.ResponseWriter, r *http.Request) { managed.catalog(w, r, resource) })
				return
			case "cluster-onboarding":
				a.AuditChange(w, r, p.Subject, "维护集群接入", "高", resource, managed.admin)
				return
			case "namespace-business":
				a.AuditChange(w, r, p.Subject, "调整命名空间归属", "高", resource, managed.namespaceAssignments)
				return
			}
		}
		if resource == "finding-analysis" {
			instance := r.URL.Query().Get("cluster")
			selected := review
			if managed != nil && (instance == "" || instance == "local") && managed.local != nil {
				if _, ok := managed.registry.Local(); !ok {
					selected = nil
				}
			}
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
		r = r.WithContext(identity.WithPrincipal(r.Context(), p))
		if resource == "ldap" || resource == "roles" || resource == "policy" || resource == "users" {
			a.AuditChange(w, r, p.Subject, "维护 "+resource, "高", resource, func(w http.ResponseWriter, r *http.Request) { a.Handle(w, r, resource, s) })
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
