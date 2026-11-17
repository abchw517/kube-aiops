package authorization

import (
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestRoleModulesPreserveScopeAndLegacyDenies(t *testing.T) {
	subject := identity.Principal{Subject: "local:alice", Provider: "local", Groups: []string{"local:operators"}}
	base := Policy{Version: PolicyVersionV1, Rules: []Rule{{
		Effect: EffectDeny, Subjects: []string{subject.Subject},
		Capabilities: []Capability{CapabilityFindingsRead},
		Scopes:       []Scope{NamespaceScope("local", "dev")},
	}}}
	role := Role{RoleSpec: RoleSpec{
		Name: "Observer", Groups: []string{"local:operators"}, Cluster: "local", Namespaces: []string{"dev"},
		Modules: []Capability{CapabilityClustersList, CapabilityNamespacesList, CapabilityFindingsList, CapabilityFindingsRead},
	}}
	policy, err := base.WithRoles([]Role{role})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		module     Capability
		scope      Scope
		collection bool
		allowed    bool
	}{
		{CapabilityClustersList, GlobalScope(), true, true},
		{CapabilityNamespacesList, ClusterScope("local"), false, true},
		{CapabilityNamespacesList, ClusterScope("other"), false, false},
		{CapabilityFindingsList, ClusterScope("local"), true, true},
		{CapabilityFindingsList, ClusterScope("local"), false, false},
		{CapabilityFindingsList, NamespaceScope("local", "dev"), false, true},
		{CapabilityFindingsList, NamespaceScope("local", "prod"), false, false},
		{CapabilityFindingsRead, NamespaceScope("local", "dev"), false, false},
		{CapabilityCorrelationsRead, NamespaceScope("local", "dev"), false, false},
	}
	for _, tt := range tests {
		got, err := policy.Authorize(t.Context(), DecisionRequest{
			Principal: subject, Capability: tt.module, Scope: tt.scope, Collection: tt.collection,
		})
		if err != nil || got.Allowed != tt.allowed {
			t.Errorf("%s %+v collection=%t: got %t, %v, want %t", tt.module, tt.scope, tt.collection, got.Allowed, err, tt.allowed)
		}
	}
	role.Namespaces = []string{GlobalCluster}
	wildcard, err := base.WithRoles([]Role{role})
	if err != nil {
		t.Fatal(err)
	}
	for _, cluster := range []string{"local", "other"} {
		decision, err := wildcard.Authorize(t.Context(), DecisionRequest{
			Principal: subject, Capability: CapabilityFindingsList, Scope: NamespaceScope(cluster, "prod"),
		})
		if err != nil || decision.Allowed != (cluster == "local") {
			t.Fatalf("wildcard namespace widened cluster scope: %s: %v, %v", cluster, decision, err)
		}
	}
}
