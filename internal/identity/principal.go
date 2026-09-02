package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	maxSubjectLength     = 256
	maxProviderLength    = 64
	maxDisplayNameLength = 256
	maxGroupLength       = 128
	maxGroups            = 64
)

var (
	// ErrUnauthenticated means the request does not carry a valid authenticated identity.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrAuthenticationUnavailable means the configured identity provider cannot authenticate safely.
	ErrAuthenticationUnavailable = errors.New("authentication unavailable")
)

// Principal is the provider-neutral authenticated identity carried through the Portal Backend.
// It intentionally excludes credentials, tokens, cookies and authorization decisions.
type Principal struct {
	Subject     string   `json:"subject"`
	Provider    string   `json:"provider"`
	DisplayName string   `json:"displayName,omitempty"`
	Groups      []string `json:"groups,omitempty"`
}

// Validate applies bounded, provider-neutral invariants before a Principal enters request context.
func (p Principal) Validate() error {
	if err := validateRequiredValue("subject", p.Subject, maxSubjectLength); err != nil {
		return err
	}
	if err := validateRequiredValue("provider", p.Provider, maxProviderLength); err != nil {
		return err
	}
	if err := validateOptionalValue("displayName", p.DisplayName, maxDisplayNameLength); err != nil {
		return err
	}
	if len(p.Groups) > maxGroups {
		return fmt.Errorf("groups exceeds maximum of %d", maxGroups)
	}
	for _, group := range p.Groups {
		if err := validateRequiredValue("group", group, maxGroupLength); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredValue(name, value string, maxLength int) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be non-empty and trimmed", name)
	}
	if len(value) > maxLength {
		return fmt.Errorf("%s exceeds maximum length of %d", name, maxLength)
	}
	if containsControl(value) {
		return fmt.Errorf("%s contains control characters", name)
	}
	return nil
}

func validateOptionalValue(name, value string, maxLength int) error {
	if value == "" {
		return nil
	}
	return validateRequiredValue(name, value, maxLength)
}

func containsControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// Authenticator is the Phase 1.4 provider adapter boundary.
// Implementations may use an external session, OIDC edge, mTLS identity, or another trusted
// mechanism, but must never place credentials inside Principal.
type Authenticator interface {
	Authenticate(context.Context, *http.Request) (Principal, error)
}

// AuthenticatorFunc adapts a function to Authenticator. It is useful for provider adapters and tests.
type AuthenticatorFunc func(context.Context, *http.Request) (Principal, error)

func (f AuthenticatorFunc) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	return f(ctx, r)
}

type principalContextKey struct{}

// WithPrincipal stores a validated Principal in request context.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated Principal attached by the AuthN middleware.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
