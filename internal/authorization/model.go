package authorization

import (
	"context"
	"fmt"
	"strings"

	"github.com/abchw517/kube-aiops/internal/identity"
)

const (
	GlobalCluster = "*"

	CapabilityClustersList    Capability = "clusters:list"
	CapabilityNamespacesList  Capability = "namespaces:list"
	CapabilityFindingsList    Capability = "findings:list"
	CapabilityFindingsSummary Capability = "findings:summary"
	CapabilityFindingsRead    Capability = "findings:read"
	CapabilityResourcesRead   Capability = "resources:read"
)

var knownCapabilities = map[Capability]struct{}{
	CapabilityClustersList:    {},
	CapabilityNamespacesList:  {},
	CapabilityFindingsList:    {},
	CapabilityFindingsSummary: {},
	CapabilityFindingsRead:    {},
	CapabilityResourcesRead:   {},
}

type Capability string

func (c Capability) Validate() error {
	if _, ok := knownCapabilities[c]; !ok {
		return fmt.Errorf("unknown capability %q", c)
	}
	return nil
}

// Scope is the normalized application-level authorization scope. It narrows access inside the
// existing safe read-only projection and is deliberately independent from Kubernetes RBAC verbs.
type Scope struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
}

func GlobalScope() Scope {
	return Scope{Cluster: GlobalCluster}
}

func ClusterScope(cluster string) Scope {
	return Scope{Cluster: strings.TrimSpace(cluster)}
}

func NamespaceScope(cluster, namespace string) Scope {
	return Scope{Cluster: strings.TrimSpace(cluster), Namespace: strings.TrimSpace(namespace)}
}

func (s Scope) Validate() error {
	if err := validateScopeValue("cluster", s.Cluster, true); err != nil {
		return err
	}
	if s.Namespace != "" {
		if err := validateScopeValue("namespace", s.Namespace, true); err != nil {
			return err
		}
	}
	return nil
}

func validateScopeValue(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be trimmed", name)
	}
	if len(value) > 253 {
		return fmt.Errorf("%s exceeds maximum length", name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

type DecisionRequest struct {
	Principal  identity.Principal
	Capability Capability
	Scope      Scope
}

func (r DecisionRequest) Validate() error {
	if err := r.Principal.Validate(); err != nil {
		return fmt.Errorf("principal: %w", err)
	}
	if err := r.Capability.Validate(); err != nil {
		return err
	}
	if err := r.Scope.Validate(); err != nil {
		return fmt.Errorf("scope: %w", err)
	}
	return nil
}

type Decision struct {
	Allowed bool
}

type Authorizer interface {
	Authorize(context.Context, DecisionRequest) (Decision, error)
}

type AuthorizerFunc func(context.Context, DecisionRequest) (Decision, error)

func (f AuthorizerFunc) Authorize(ctx context.Context, request DecisionRequest) (Decision, error) {
	return f(ctx, request)
}
