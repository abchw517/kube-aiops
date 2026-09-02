package identity

import (
	"context"
	"strings"
	"testing"
)

func TestPrincipalValidate(t *testing.T) {
	principal := Principal{
		Subject:     "user:alice",
		Provider:    "oidc",
		DisplayName: "Alice",
		Groups:      []string{"sre", "platform"},
	}
	if err := principal.Validate(); err != nil {
		t.Fatalf("expected valid principal, got %v", err)
	}
}

func TestPrincipalRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name      string
		principal Principal
	}{
		{name: "empty subject", principal: Principal{Provider: "oidc"}},
		{name: "untrimmed subject", principal: Principal{Subject: " alice ", Provider: "oidc"}},
		{name: "control character", principal: Principal{Subject: "alice\nadmin", Provider: "oidc"}},
		{name: "empty provider", principal: Principal{Subject: "alice"}},
		{name: "oversized display name", principal: Principal{Subject: "alice", Provider: "oidc", DisplayName: strings.Repeat("x", maxDisplayNameLength+1)}},
		{name: "empty group", principal: Principal{Subject: "alice", Provider: "oidc", Groups: []string{""}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.principal.Validate(); err == nil {
				t.Fatal("expected principal validation to fail")
			}
		})
	}
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	principal := Principal{Subject: "user:alice", Provider: "oidc"}
	ctx := WithPrincipal(context.Background(), principal)
	got, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("expected principal in context")
	}
	if got.Subject != principal.Subject || got.Provider != principal.Provider {
		t.Fatalf("unexpected principal: %#v", got)
	}
}
