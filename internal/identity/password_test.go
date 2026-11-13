package identity

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthenticateLocal(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("example-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	accounts := []LocalAccount{{Username: "alice", Hash: string(hash), Groups: []string{"sre"}}}
	principal, err := AuthenticateLocal(accounts, "alice", "example-password")
	if err != nil || principal.Subject != "alice" || principal.Provider != "local" || len(principal.Groups) != 1 {
		t.Fatalf("unexpected authentication result: %#v, %v", principal, err)
	}
	for _, pair := range [][2]string{{"alice", ""}, {"alice", "wrong"}, {"other", "example-password"}, {"", "example-password"}} {
		if _, err := AuthenticateLocal(accounts, pair[0], pair[1]); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("credentials %#v: expected unauthenticated, got %v", pair, err)
		}
	}
}

func TestAuthenticateLDAPRejectsUnsafeConfiguration(t *testing.T) {
	for _, cfg := range []LDAPConfig{
		{URL: "ldap://dc.example.com:389"},
		{URL: "ldaps://dc.example.com:636", BaseDN: "dc=example,dc=com", BindDN: "cn=service,dc=example,dc=com", BindPassword: "secret", UserFilter: "(uid=%s)(uid=%s)"},
	} {
		if _, err := AuthenticateLDAP(cfg, "alice", "password"); !errors.Is(err, ErrAuthenticationUnavailable) {
			t.Fatalf("expected unavailable for invalid configuration, got %v", err)
		}
	}
	if _, err := AuthenticateLDAP(LDAPConfig{}, "alice", ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("empty passwords must be rejected: %v", err)
	}
}
