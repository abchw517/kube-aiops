package authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/abchw517/kube-aiops/internal/identity"
)

const PolicyVersionV1 = "v1"

type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Policy is a versioned, credential-free authorization policy suitable for deterministic local and
// CI evaluation. Enterprise identity-provider integration remains outside this policy format.
type Policy struct {
	Version string `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Rule struct {
	Effect       Effect       `json:"effect"`
	Subjects     []string     `json:"subjects,omitempty"`
	Groups       []string     `json:"groups,omitempty"`
	Capabilities []Capability `json:"capabilities"`
	Scopes       []Scope      `json:"scopes"`
}

type PolicyAuthorizer struct {
	rules []Rule
}

func ParsePolicyJSON(data []byte) (*PolicyAuthorizer, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("decode authorization policy: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	return NewPolicyAuthorizer(policy)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("authorization policy contains multiple JSON values")
		}
		return fmt.Errorf("decode authorization policy trailer: %w", err)
	}
	return nil
}

func NewPolicyAuthorizer(policy Policy) (*PolicyAuthorizer, error) {
	if policy.Version != PolicyVersionV1 {
		return nil, fmt.Errorf("unsupported authorization policy version %q", policy.Version)
	}
	for index, rule := range policy.Rules {
		if err := validateRule(rule); err != nil {
			return nil, fmt.Errorf("authorization rule %d: %w", index, err)
		}
	}

	rules := append([]Rule(nil), policy.Rules...)
	return &PolicyAuthorizer{rules: rules}, nil
}

func validateRule(rule Rule) error {
	if rule.Effect != EffectAllow && rule.Effect != EffectDeny {
		return fmt.Errorf("effect must be %q or %q", EffectAllow, EffectDeny)
	}
	if len(rule.Subjects) == 0 && len(rule.Groups) == 0 {
		return fmt.Errorf("at least one subject or group selector is required")
	}
	for _, subject := range rule.Subjects {
		if err := validateSelector("subject", subject); err != nil {
			return err
		}
	}
	for _, group := range rule.Groups {
		if err := validateSelector("group", group); err != nil {
			return err
		}
	}
	if len(rule.Capabilities) == 0 {
		return fmt.Errorf("at least one capability is required")
	}
	for _, capability := range rule.Capabilities {
		if err := capability.Validate(); err != nil {
			return err
		}
	}
	if len(rule.Scopes) == 0 {
		return fmt.Errorf("at least one scope is required")
	}
	for _, scope := range rule.Scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateSelector(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s selector must be non-empty and trimmed", name)
	}
	if len(value) > 256 {
		return fmt.Errorf("%s selector exceeds maximum length", name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s selector contains control characters", name)
		}
	}
	return nil
}

func (a *PolicyAuthorizer) Authorize(_ context.Context, request DecisionRequest) (Decision, error) {
	if err := request.Validate(); err != nil {
		return Decision{}, fmt.Errorf("invalid authorization request: %w", err)
	}

	allowed := false
	for _, rule := range a.rules {
		if !selectorMatches(rule, request.Principal) ||
			!capabilityMatches(rule.Capabilities, request.Capability) ||
			!scopesMatch(rule.Scopes, request.Scope) {
			continue
		}
		if rule.Effect == EffectDeny {
			return Decision{Allowed: false}, nil
		}
		allowed = true
	}
	return Decision{Allowed: allowed}, nil
}

func selectorMatches(rule Rule, principal identity.Principal) bool {
	for _, subject := range rule.Subjects {
		if subject == principal.Subject {
			return true
		}
	}
	for _, expected := range rule.Groups {
		for _, actual := range principal.Groups {
			if expected == actual {
				return true
			}
		}
	}
	return false
}

func capabilityMatches(capabilities []Capability, requested Capability) bool {
	for _, capability := range capabilities {
		if capability == requested {
			return true
		}
	}
	return false
}

func scopesMatch(grants []Scope, requested Scope) bool {
	for _, grant := range grants {
		if scopeMatches(grant, requested) {
			return true
		}
	}
	return false
}

func scopeMatches(grant, requested Scope) bool {
	if grant.Cluster != GlobalCluster && grant.Cluster != requested.Cluster {
		return false
	}
	if requested.Namespace == "" {
		return grant.Namespace == ""
	}
	if grant.Namespace == "" {
		return false
	}
	return grant.Namespace == GlobalCluster || grant.Namespace == requested.Namespace
}
