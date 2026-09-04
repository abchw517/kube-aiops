package security

import (
	"context"
	"errors"
	"net/http"
	"testing"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
)

func TestParseMode(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   string
		want    Mode
		wantErr bool
	}{
		{name: "development", input: "development", want: ModeDevelopment},
		{name: "production", input: " production ", want: ModeProduction},
		{name: "case normalized", input: "DEVELOPMENT", want: ModeDevelopment},
		{name: "empty rejected", input: "", wantErr: true},
		{name: "unsupported rejected", input: "permissive", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseMode(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMode() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("ParseMode() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBundleValidateForProduction(t *testing.T) {
	complete := testBundle()
	if err := complete.ValidateForProduction(); err != nil {
		t.Fatalf("complete bundle rejected: %v", err)
	}

	for _, test := range []struct {
		name      string
		component string
		mutate    func(*Bundle)
	}{
		{name: "authenticator", component: "authenticator", mutate: func(b *Bundle) { b.Authenticator = nil }},
		{name: "authorizer", component: "authorizer", mutate: func(b *Bundle) { b.Authorizer = nil }},
		{name: "audit sink", component: "audit_sink", mutate: func(b *Bundle) { b.AuditSink = nil }},
		{name: "sanitizer", component: "sanitizer", mutate: func(b *Bundle) { b.Sanitizer = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := testBundle()
			test.mutate(&bundle)
			err := bundle.ValidateForProduction()
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			if validationErr.Component != test.component {
				t.Fatalf("component = %q, want %q", validationErr.Component, test.component)
			}
		})
	}
}

func TestBundleRejectsTypedNilComponent(t *testing.T) {
	bundle := testBundle()
	var authorizer *authorization.PolicyAuthorizer
	bundle.Authorizer = authorizer

	err := bundle.ValidateForProduction()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Component != "authorizer" {
		t.Fatalf("error = %v, want typed-nil authorizer rejection", err)
	}
}

func testBundle() Bundle {
	return Bundle{
		Authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
			return identity.Principal{Subject: "test-user", Provider: "test"}, nil
		}),
		Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
			return authorization.Decision{Allowed: true}, nil
		}),
		AuditSink: internalaudit.SinkFunc(func(context.Context, internalaudit.Event) error { return nil }),
		Sanitizer: sanitizer.Default(),
	}
}
