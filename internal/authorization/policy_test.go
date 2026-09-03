package authorization

import (
	"context"
	"testing"

	"github.com/abchw517/kube-aiops/internal/identity"
)

func testPrincipal() identity.Principal {
	return identity.Principal{Subject: "user:alice", Provider: "test", Groups: []string{"sre"}}
}

func mustPolicyAuthorizer(t *testing.T, policy Policy) *PolicyAuthorizer {
	t.Helper()
	authorizer, err := NewPolicyAuthorizer(policy)
	if err != nil {
		t.Fatalf("NewPolicyAuthorizer() error = %v", err)
	}
	return authorizer
}

func TestPolicyAuthorizerExactAllow(t *testing.T) {
	authorizer := mustPolicyAuthorizer(t, Policy{
		Version: PolicyVersionV1,
		Rules: []Rule{{
			Effect:       EffectAllow,
			Subjects:     []string{"user:alice"},
			Capabilities: []Capability{CapabilityFindingsRead},
			Scopes:       []Scope{NamespaceScope("local", "dev")},
		}},
	})

	decision, err := authorizer.Authorize(context.Background(), DecisionRequest{
		Principal:  testPrincipal(),
		Capability: CapabilityFindingsRead,
		Scope:      NamespaceScope("local", "dev"),
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("expected allow, decision=%+v err=%v", decision, err)
	}
}

func TestPolicyAuthorizerMissingCapabilityDenies(t *testing.T) {
	authorizer := mustPolicyAuthorizer(t, Policy{
		Version: PolicyVersionV1,
		Rules: []Rule{{
			Effect:       EffectAllow,
			Groups:       []string{"sre"},
			Capabilities: []Capability{CapabilityFindingsList},
			Scopes:       []Scope{NamespaceScope("local", "dev")},
		}},
	})

	decision, err := authorizer.Authorize(context.Background(), DecisionRequest{
		Principal:  testPrincipal(),
		Capability: CapabilityResourcesRead,
		Scope:      NamespaceScope("local", "dev"),
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("missing capability must deny")
	}
}

func TestPolicyAuthorizerClusterOnlyGrantDoesNotAuthorizeNamespacedRequest(t *testing.T) {
	authorizer := mustPolicyAuthorizer(t, Policy{
		Version: PolicyVersionV1,
		Rules: []Rule{{
			Effect:       EffectAllow,
			Subjects:     []string{"user:alice"},
			Capabilities: []Capability{CapabilityResourcesRead},
			Scopes:       []Scope{ClusterScope("local")},
		}},
	})

	decision, err := authorizer.Authorize(context.Background(), DecisionRequest{
		Principal:  testPrincipal(),
		Capability: CapabilityResourcesRead,
		Scope:      NamespaceScope("local", "dev"),
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("cluster-only grant must not authorize namespaced request")
	}
}

func TestPolicyAuthorizerExplicitNamespaceWildcard(t *testing.T) {
	authorizer := mustPolicyAuthorizer(t, Policy{
		Version: PolicyVersionV1,
		Rules: []Rule{{
			Effect:       EffectAllow,
			Groups:       []string{"sre"},
			Capabilities: []Capability{CapabilityFindingsList},
			Scopes:       []Scope{{Cluster: "local", Namespace: GlobalCluster}},
		}},
	})

	decision, err := authorizer.Authorize(context.Background(), DecisionRequest{
		Principal:  testPrincipal(),
		Capability: CapabilityFindingsList,
		Scope:      NamespaceScope("local", "prod"),
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("expected explicit namespace wildcard allow, decision=%+v err=%v", decision, err)
	}
}

func TestPolicyAuthorizerDenyOverridesAllow(t *testing.T) {
	authorizer := mustPolicyAuthorizer(t, Policy{
		Version: PolicyVersionV1,
		Rules: []Rule{
			{
				Effect:       EffectAllow,
				Groups:       []string{"sre"},
				Capabilities: []Capability{CapabilityFindingsRead},
				Scopes:       []Scope{{Cluster: "local", Namespace: GlobalCluster}},
			},
			{
				Effect:       EffectDeny,
				Subjects:     []string{"user:alice"},
				Capabilities: []Capability{CapabilityFindingsRead},
				Scopes:       []Scope{NamespaceScope("local", "prod")},
			},
		},
	})

	decision, err := authorizer.Authorize(context.Background(), DecisionRequest{
		Principal:  testPrincipal(),
		Capability: CapabilityFindingsRead,
		Scope:      NamespaceScope("local", "prod"),
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("deny rule must override allow rule")
	}
}

func TestPolicyAuthorizerEmptyPolicyDenies(t *testing.T) {
	authorizer := mustPolicyAuthorizer(t, Policy{Version: PolicyVersionV1})
	decision, err := authorizer.Authorize(context.Background(), DecisionRequest{
		Principal:  testPrincipal(),
		Capability: CapabilityClustersList,
		Scope:      GlobalScope(),
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("empty policy must deny")
	}
}

func TestParsePolicyJSONFailsClosedOnMalformedPolicy(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"version":"v1","rules":[],"unexpected":true}`),
		[]byte(`{"version":"v1","rules":[]} {}`),
		[]byte(`{"version":"v2","rules":[]}`),
	}
	for _, data := range cases {
		if _, err := ParsePolicyJSON(data); err == nil {
			t.Fatalf("expected malformed policy to fail: %s", data)
		}
	}
}
