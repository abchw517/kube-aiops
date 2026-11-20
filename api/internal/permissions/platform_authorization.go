package permissions

import (
	"context"

	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
)

func validAccessScope(scope accessScope) bool {
	if scope.Cluster == "*" || scope.Namespace == "*" || scope.Business == "*" {
		return false
	}
	if scope.Cluster == "" && scope.Namespace != "" {
		return false
	}
	switch scope.Environment {
	case "development", "testing":
		return scope.Business == ""
	case "staging", "production":
		return scope.Cluster != "" && scope.Namespace != "" && scope.Business != ""
	default:
		return false
	}
}

type ClusterScopeResolver func(context.Context, string) (string, error)

type NamespaceBusinessResolver func(context.Context, string, string, string) (bool, error)

func (a *Admin) SetScopeResolvers(cluster ClusterScopeResolver, business NamespaceBusinessResolver) {
	a.clusterEnvironment, a.namespaceBusiness = cluster, business
}

func (a *Admin) authorizePlatform(ctx context.Context, r authorization.DecisionRequest) (authorization.Decision, error) {
	allowed, err := a.AuthorizeOperation(ctx, r.Principal, diagnosticCodes[r.Capability], r.Capability, r.Scope, r.Collection)
	return authorization.Decision{Allowed: allowed}, err
}
