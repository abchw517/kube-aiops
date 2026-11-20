package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type interfaceGrant struct {
	Code  string      `json:"code"`
	Scope accessScope `json:"scope"`
}

type effectiveGrant struct {
	interfaceGrant
	Sources []string `json:"sources"`
}

var diagnosticCodes = map[authorization.Capability]string{
	authorization.CapabilityClustersList:     "diagnosis.discovery.clusters",
	authorization.CapabilityNamespacesList:   "diagnosis.discovery.namespaces",
	authorization.CapabilityResourcesRead:    "diagnosis.discovery.resource",
	authorization.CapabilityFindingsList:     "diagnosis.finding.list",
	authorization.CapabilityFindingsSummary:  "diagnosis.finding.summary",
	authorization.CapabilityFindingsRead:     catalog.FindingRead,
	authorization.CapabilityCorrelationsRead: "diagnosis.evidence.read",
}

func legacyDiagnosticScope(code string) bool {
	switch code {
	case "diagnosis.discovery.clusters", "diagnosis.discovery.namespaces", "diagnosis.finding.list", "diagnosis.finding.summary", catalog.FindingRead:
		return true
	}
	return false
}

func (state platformState) effectiveGrants(account platformAccount) []effectiveGrant {
	byGrant := map[interfaceGrant][]string{}
	for id, sources := range state.roleSources(account) {
		role := state.role(id)
		for _, grant := range role.Grants {
			for _, source := range sources {
				byGrant[grant] = append(byGrant[grant], source+" → "+role.Name)
			}
		}
	}
	out := make([]effectiveGrant, 0, len(byGrant))
	for grant, sources := range byGrant {
		slices.Sort(sources)
		out = append(out, effectiveGrant{grant, slices.Compact(sources)})
	}
	slices.SortFunc(out, func(a, b effectiveGrant) int {
		return strings.Compare(grantOrder(a.interfaceGrant), grantOrder(b.interfaceGrant))
	})
	return out
}

func grantOrder(g interfaceGrant) string {
	return strings.Join([]string{g.Code, g.Scope.Environment, g.Scope.Cluster, g.Scope.Business, g.Scope.Namespace}, "\x00")
}

func (a *Admin) validateInterfaceGrants(ctx context.Context, grants []interfaceGrant) error {
	for _, grant := range grants {
		point, exists := catalog.Find(grant.Code)
		if !exists || point.Restricted {
			return errors.New("权限点不存在或需要固定管理身份")
		}
		scope := grant.Scope
		switch point.Scope {
		case "平台全局", "仅本人":
			if scope != (accessScope{}) {
				return errors.New("全局和本人权限不能配置资源范围")
			}
			continue
		case "集群实例":
			if scope.Namespace != "" || scope.Business != "" || scope.Cluster == "" ||
				scope.Cluster == "*" || !slices.Contains([]string{"development", "testing", "staging", "production"}, scope.Environment) {
				return errors.New("集群权限必须绑定环境和实例")
			}
		default:
			if !validAccessScope(scope) {
				return errors.New("授权范围无效，生产和预发布须指定实例、业务线与命名空间")
			}
		}
		if scope.Cluster == "" {
			continue
		}
		if a.clusterEnvironment == nil {
			return errors.New("集群目录暂不可用")
		}
		env, err := a.clusterEnvironment(ctx, scope.Cluster)
		if err != nil || env != scope.Environment {
			return errors.New("所选实例不属于该环境或集群目录不可用")
		}
		if scope.Business != "" {
			if a.namespaceBusiness == nil {
				return errors.New("命名空间目录暂不可用")
			}
			ok, err := a.namespaceBusiness(ctx, scope.Cluster, scope.Namespace, scope.Business)
			if err != nil || !ok {
				return errors.New("命名空间未归属于所选业务线")
			}
		}
	}
	return nil
}

func UsesInterfaceRoles(p identity.Principal) bool {
	return p.Provider == guestProvider || p.Provider == employeeProvider
}

// HasScopedOperation gates collection entry for platform roles. Legacy policies
// are evaluated against each real resource, as they may only contain scoped rules.
func (a *Admin) HasScopedOperation(ctx context.Context, p identity.Principal, code string) bool {
	if !UsesInterfaceRoles(p) {
		return !MustChangePassword(p)
	}
	ok, err := a.AuthorizeOperation(ctx, p, code, "", authorization.GlobalScope(), true)
	return err == nil && ok
}

// AuthorizeOperation preserves the existing administrator and imported policy paths.
// Platform accounts use independently scoped role grants.
func (a *Admin) AuthorizeOperation(ctx context.Context, actor identity.Principal, code string, legacy authorization.Capability, scope authorization.Scope, collection bool) (bool, error) {
	point, exists := catalog.Find(code)
	if !exists || MustChangePassword(actor) {
		return false, nil
	}
	if IsAdministrator(actor) {
		return true, nil
	}
	if point.Restricted {
		return false, nil
	}
	request := authorization.DecisionRequest{Principal: actor, Capability: legacy, Scope: scope, Collection: collection}
	switch actor.Provider {
	case guestProvider, employeeProvider:
		if legacy != "" {
			a.mu.RLock()
			denied := a.policy != nil && a.policy.Denies(request)
			a.mu.RUnlock()
			if denied {
				return false, nil
			}
		}
		return a.platformOperation(ctx, actor, point, scope, collection)
	default:
		if legacy == "" {
			return false, nil
		}
		decision, err := a.Authorize(ctx, request)
		return decision.Allowed, err
	}
}

func (a *Admin) platformOperation(ctx context.Context, actor identity.Principal, point catalog.Point, scope authorization.Scope, collection bool) (bool, error) {
	state, err := a.platform(ctx)
	if err != nil {
		return false, err
	}
	account := state.Accounts[actor.Subject]
	if account.ID == "" || account.Disabled || account.DirectoryDisabled || account.MustChange || account.Generation != actor.SessionVersion {
		return false, nil
	}

	var scopes []accessScope
	for id := range state.roleSources(account) {
		role := state.role(id)
		if legacyDiagnosticScope(point.Code) {
			scopes = append(scopes, role.Scopes...)
		}
		for _, grant := range role.Grants {
			if grant.Code == point.Code {
				scopes = append(scopes, grant.Scope)
			}
		}
	}
	if point.Scope == "平台全局" {
		return scope == authorization.GlobalScope() && len(scopes) > 0, nil
	}
	if point.Scope == "仅本人" {
		// Callers bind the credential operation to the authenticated subject and actual source.
		return len(scopes) > 0 && (collection || scope.Cluster != "" && scope.Cluster != authorization.GlobalCluster && scope.Namespace != ""), nil
	}
	if scope.Cluster == authorization.GlobalCluster {
		return collection && len(scopes) > 0, nil
	}
	if len(scopes) == 0 || a.clusterEnvironment == nil || scope.Cluster == "" {
		return false, nil
	}
	env, err := a.clusterEnvironment(ctx, scope.Cluster)
	if err != nil || env == "" {
		return false, err
	}
	for _, grant := range scopes {
		if grant.Environment != env || grant.Cluster != "" && grant.Cluster != scope.Cluster ||
			grant.Namespace != "" && grant.Namespace != scope.Namespace && !(scope.Namespace == "" && (collection || point.Code == "diagnosis.discovery.clusters")) {
			continue
		}
		if grant.Business != "" {
			if a.namespaceBusiness == nil {
				continue
			}
			ok, err := a.namespaceBusiness(ctx, grant.Cluster, grant.Namespace, grant.Business)
			if err != nil {
				return false, err
			}
			if !ok {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

func auditGrantDiff(before, after []interfaceGrant) string {
	diff := struct {
		Added   []interfaceGrant `json:"added,omitempty"`
		Removed []interfaceGrant `json:"removed,omitempty"`
	}{}
	for _, grant := range after {
		if !slices.Contains(before, grant) {
			diff.Added = append(diff.Added, grant)
		}
	}
	for _, grant := range before {
		if !slices.Contains(after, grant) {
			diff.Removed = append(diff.Removed, grant)
		}
	}
	if len(diff.Added)+len(diff.Removed) == 0 {
		return ""
	}
	data, _ := json.Marshal(diff)
	return string(data)
}
