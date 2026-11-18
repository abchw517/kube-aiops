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
	switch r.Capability {
	case authorization.CapabilityClustersList, authorization.CapabilityNamespacesList, authorization.CapabilityFindingsList,
		authorization.CapabilityFindingsSummary, authorization.CapabilityFindingsRead:
	default:
		return authorization.Decision{}, nil
	}
	state, err := a.platform(ctx)
	if err != nil {
		return authorization.Decision{}, err
	}
	account := state.Accounts[r.Principal.Subject]
	if account.ID == "" || account.Disabled || account.DirectoryDisabled || account.MustChange || account.Generation != r.Principal.SessionVersion {
		return authorization.Decision{}, nil
	}
	scopes := state.effective(account)
	if r.Scope.Cluster == authorization.GlobalCluster {
		return authorization.Decision{Allowed: r.Collection && len(scopes) > 0}, nil
	}
	if a.clusterEnvironment == nil {
		return authorization.Decision{}, nil
	}
	env, err := a.clusterEnvironment(ctx, r.Scope.Cluster)
	if err != nil || env == "" {
		return authorization.Decision{}, err
	}
	for _, scope := range scopes {
		if scope.Environment != env || scope.Cluster != "" && scope.Cluster != r.Scope.Cluster {
			continue
		}
		if scope.Business != "" {
			if a.namespaceBusiness == nil {
				continue
			}
			allowed, err := a.namespaceBusiness(ctx, scope.Cluster, scope.Namespace, scope.Business)
			if err != nil {
				return authorization.Decision{}, err
			}
			if !allowed {
				continue
			}
		}
		if scope.Namespace == "" || scope.Namespace == r.Scope.Namespace ||
			r.Scope.Namespace == "" && (r.Collection || r.Capability == authorization.CapabilityClustersList) {
			return authorization.Decision{Allowed: true}, nil
		}
	}
	return authorization.Decision{}, nil
}
