package portal

import (
	"net/http"

	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func adminReadCode(r *http.Request, resource string) string {
	if r.Method != http.MethodGet {
		return ""
	}
	switch resource {
	case "permission-catalog":
		return catalog.CatalogRead
	case "environments":
		return "resources.environment.list"
	case "business-lines":
		return "resources.business.list"
	case "cluster-onboarding":
		return "resources.cluster.list"
	case "namespace-business":
		return "resources.ownership.list"
	case "ldap":
		return "system.ldap.read"
	case "operations":
		if r.URL.Query().Get("retention") == "1" {
			return "system.retention.read"
		}
		if r.URL.Query().Get("record") != "" || r.URL.Query().Get("batch") != "" {
			return "system.audit.record"
		}
		return "system.audit.list"
	}
	return ""
}

func canEnterAdmin(a *permissions.Admin, r *http.Request, p identity.Principal, resource string) bool {
	if resource == "password" {
		return permissions.CanChangePassword(p)
	}
	if permissions.IsAdministrator(p) {
		return true
	}
	allow := func(code string, scope authorization.Scope, collection bool) bool {
		allowed, err := a.AuthorizeOperation(r.Context(), p, code, "", scope, collection)
		return err == nil && allowed
	}
	global := authorization.GlobalScope()
	switch resource {
	case "finding-analysis":
		return allow(catalog.ReviewRead, global, true) || allow(catalog.ReviewRun, global, true) ||
			allow(catalog.ReviewChat, global, true) || allow(catalog.ReviewSave, global, true)
	case "accounts":
		return r.Method == http.MethodGet && (allow("system.account.list", global, false) || allow("system.binding.read", global, false))
	case "access":
		return r.Method == http.MethodGet && (allow("system.role.list", global, false) || allow("system.group.list", global, false))
	case "namespace-business":
		return allow(adminReadCode(r, resource), authorization.ClusterScope(r.URL.Query().Get("cluster")), false)
	case "cluster-onboarding":
		return allow(adminReadCode(r, resource), global, true)
	default:
		return allow(adminReadCode(r, resource), global, false)
	}
}
