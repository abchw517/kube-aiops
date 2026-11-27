package portal

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/abchw517/kube-aiops/api/internal/holmes"
	"net/http"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
)

func wrapAdmin(a *permissions.Admin, wikis *wiki.Service, s *identity.Sessions, next http.Handler, review *diagnosis.Review, managed *onboarding, incidentServers ...*incident.Server) http.Handler {
	mux := http.NewServeMux()
	var incidents *incident.Server
	if len(incidentServers) > 0 {
		incidents = incidentServers[0]
		if incidents != nil {
			incidentRoutes := http.NewServeMux()
			incidents.Register(incidentRoutes)
			mux.Handle("/api/v1/diagnostics/", httpapi.WithRequestMetadata(incidentRoutes))
		}
	}
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
			read := func(resource string) bool { return canEnterAdmin(a, r, p, resource) }
			status := map[string]bool{"admin": admin, "findingAI": review.Enabled() && read("finding-analysis"), "permissionCatalog": read("permission-catalog"), "auditRead": read("operations"), "mustChangePassword": permissions.MustChangePassword(p), "canChangePassword": permissions.CanChangePassword(p), "superAdministrator": p.Subject == "local:admin" && p.Provider == permissions.AdminProvider}
			for field, code := range map[string]string{
				"wikiDraft": "knowledge.candidate.create", "wikiCandidates": "knowledge.candidate.list",
				"wikiGovernance": "knowledge.governance.list",
			} {
				status[field], _ = a.AuthorizeOperation(r.Context(), p, code, "", authorization.GlobalScope(), true)
			}
			if incidents != nil {
				status["incidents"], _ = incidents.Authorize(r.Context(), p, incident.ListPermission, incident.Scope{}, true)
				status["incidentCreate"], _ = incidents.Authorize(r.Context(), p, incident.CreatePermission, incident.Scope{}, true)
				for field, code := range map[string]string{
					"diagnosticDatasources": incident.ConnectorRead, "diagnosticDatasourceWrite": incident.ConnectorWrite,
					"diagnosticDatasourceTest": incident.ConnectorTest, "diagnosticPolicies": incident.PolicyRead,
					"diagnosticPolicyWrite": incident.PolicyWrite,
				} {
					status[field], _ = incidents.Authorize(r.Context(), p, code, incident.Scope{}, true)
				}
			}
			status["reviewAccess"] = read("finding-analysis")
			status["interfaceRoleAccount"] = permissions.UsesInterfaceRoles(p)
			status["findingChat"], _ = a.AuthorizeOperation(r.Context(), p, catalog.ReviewChat, "", authorization.GlobalScope(), true)
			status["wikiAsk"] = a.HasScopedOperation(r.Context(), p, catalog.WikiAsk)
			if managed != nil {
				status["manageEnvironments"] = read("environments")
				status["manageBusinessLines"] = read("business-lines")
				status["manageClusters"] = read("cluster-onboarding")
			}
			adminhttp.WriteJSON(w, status)
			return
		}
		if !canEnterAdmin(a, r, p, resource) {
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
		if resource == "ai-services" || resource == "ai-skills" {
			if !admin || permissions.MustChangePassword(p) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if resource == "ai-skills" {
				a.AuditChange(w, r, p.Subject, "维护 AI 技能", "中", resource, func(w http.ResponseWriter, r *http.Request) { serveAISkills(w, r, wikis, p) })
			} else {
				a.AuditChange(w, r, p.Subject, "维护 AI 服务", "中", resource, func(w http.ResponseWriter, r *http.Request) {
					serveAIServices(w, r, func(ctx context.Context, scope holmes.Scope) error {
						if incidents == nil {
							return incident.ErrNotFound
						}
						return incidents.ValidateHolmesAlertScope(ctx, p, scope)
					})
				})
			}
			return
		}
		if resource == "permission-catalog" && r.Method == http.MethodGet {
			catalog.Serve(w)
			return
		}
		if managed != nil {
			switch resource {
			case environmentKind, businessKind:
				a.AuditChange(w, r, p.Subject, "维护资源目录", "中", resource, func(w http.ResponseWriter, r *http.Request) { managed.catalog(w, r, resource) })
				return
			case "cluster-onboarding":
				a.AuditChange(w, r, p.Subject, "维护集群接入", "高", resource, func(w http.ResponseWriter, r *http.Request) {
					managed.admin(w, r.WithContext(withClusterVisibility(r.Context(), func(id string) bool {
						allowed, err := a.AuthorizeOperation(r.Context(), p, "resources.cluster.list", "", authorization.ClusterScope(id), false)
						return err == nil && allowed
					})))
				})
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
				ctx := wikis.WithFindingLookup(identity.WithPrincipal(r.Context(), p), p)
				ctx = diagnosis.WithReviewAuthorization(ctx, func(ctx context.Context, code string, item finding.Finding) bool {
					scope := authorization.NamespaceScope(item.Cluster, item.Namespace)
					if !admin && item.Namespace == "" {
						return false
					}
					findingAllowed, findingErr := a.AuthorizeOperation(ctx, p, catalog.FindingRead, authorization.CapabilityFindingsRead, scope, false)
					allowed, err := a.AuthorizeOperation(ctx, p, code, "", scope, false)
					return findingErr == nil && findingAllowed && err == nil && allowed
				})
				ctx = wiki.WithSessionRecheck(ctx, func(ctx context.Context) bool {
					current, err := s.Authenticate(ctx, r)
					return err == nil && current.Subject == p.Subject && current.Provider == p.Provider
				})
				selected.ServeHTTP(w, r.WithContext(wikis.WithFindingPublish(ctx, p)))
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
	wikis.SetFindingLookup(func(ctx context.Context, cluster, id string) (finding.Finding, string, error) {
		var backend httpapi.Backend
		if managed != nil {
			if target, ok := managed.scopedCluster(cluster); ok {
				backend = target.Backend
			}
		} else if cluster == "local" && review != nil {
			backend = review.Backend
		}
		if backend == nil {
			return finding.Finding{}, "", errors.New("finding source unavailable")
		}
		item, err := backend.GetFinding(ctx, id)
		return item, diagnosis.ReviewKey(item), err
	})
	wikis.Register(mux, s, func(ctx context.Context, prompt string, data []byte) (string, error) {
		// Only the excerpts already selected by the Wiki ACL enter this tool-free deployment.
		limited, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		scope := holmes.Scope{Source: "wiki", SourceID: "published-excerpts"}
		answer, err := holmes.Chat(limited, scope, prompt, json.RawMessage(data), nil)
		if err == nil && json.Valid([]byte(answer)) {
			return answer, nil
		}
		return review.AskWiki(ctx, prompt, data)
	})
	mux.Handle("/", next)
	return mux
}

func sameAdminOrigin(r *http.Request, expected string) bool {
	origins := r.Header.Values("Origin")
	return len(origins) == 1 && origins[0] == expected
}
