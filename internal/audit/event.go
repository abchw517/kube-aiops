package audit

import (
	"fmt"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
)

const (
	maxRequestIDLength    = 128
	maxRoutePatternLength = 256
	maxPrincipalLength    = 256
	maxProviderLength     = 64
	maxScopeLength        = 253
	maxLatencyMS          = int64((24 * time.Hour) / time.Millisecond)
)

type Outcome string

const (
	OutcomeSuccess             Outcome = "success"
	OutcomeUnauthenticated     Outcome = "unauthenticated"
	OutcomeDenied              Outcome = "denied"
	OutcomeSecurityUnavailable Outcome = "security_unavailable"
	OutcomeNotFound            Outcome = "not_found"
	OutcomeInvalidRequest      Outcome = "invalid_request"
	OutcomeBackendError        Outcome = "backend_error"
)

var knownOutcomes = map[Outcome]struct{}{
	OutcomeSuccess:             {},
	OutcomeUnauthenticated:     {},
	OutcomeDenied:              {},
	OutcomeSecurityUnavailable: {},
	OutcomeNotFound:            {},
	OutcomeInvalidRequest:      {},
	OutcomeBackendError:        {},
}

// Event is the fixed allowlisted Phase 1.4.3 audit contract. It intentionally excludes
// credentials, request/response bodies, raw URLs/query strings, Finding IDs/payloads,
// Kubernetes objects and provider/sink error strings.
type Event struct {
	Timestamp         time.Time                `json:"timestamp"`
	RequestID         string                   `json:"requestID"`
	CorrelationID     string                   `json:"correlationID"`
	RoutePattern      string                   `json:"routePattern"`
	Capability        authorization.Capability `json:"capability"`
	PrincipalSubject  string                   `json:"principalSubject,omitempty"`
	PrincipalProvider string                   `json:"principalProvider,omitempty"`
	Cluster           string                   `json:"cluster,omitempty"`
	Namespace         string                   `json:"namespace,omitempty"`
	Outcome           Outcome                  `json:"outcome"`
	HTTPStatus        int                      `json:"httpStatus"`
	LatencyMS         int64                    `json:"latencyMs"`
}

func (o Outcome) Validate() error {
	if _, ok := knownOutcomes[o]; !ok {
		return fmt.Errorf("unknown audit outcome %q", o)
	}
	return nil
}

func (e Event) Validate() error {
	if e.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	if err := validateBoundedText("requestID", e.RequestID, maxRequestIDLength, true); err != nil {
		return err
	}
	if err := validateBoundedText("correlationID", e.CorrelationID, maxRequestIDLength, true); err != nil {
		return err
	}
	if err := validateBoundedText("routePattern", e.RoutePattern, maxRoutePatternLength, true); err != nil {
		return err
	}
	if err := e.Capability.Validate(); err != nil {
		return fmt.Errorf("capability: %w", err)
	}
	if err := e.Outcome.Validate(); err != nil {
		return err
	}

	if (e.PrincipalSubject == "") != (e.PrincipalProvider == "") {
		return fmt.Errorf("principal subject and provider must be present together")
	}
	if err := validateBoundedText("principalSubject", e.PrincipalSubject, maxPrincipalLength, false); err != nil {
		return err
	}
	if err := validateBoundedText("principalProvider", e.PrincipalProvider, maxProviderLength, false); err != nil {
		return err
	}

	if e.Namespace != "" && e.Cluster == "" {
		return fmt.Errorf("cluster is required when namespace is present")
	}
	if err := validateBoundedText("cluster", e.Cluster, maxScopeLength, false); err != nil {
		return err
	}
	if err := validateBoundedText("namespace", e.Namespace, maxScopeLength, false); err != nil {
		return err
	}
	if e.HTTPStatus < 100 || e.HTTPStatus > 599 {
		return fmt.Errorf("httpStatus is outside the valid HTTP status range")
	}
	if e.LatencyMS < 0 || e.LatencyMS > maxLatencyMS {
		return fmt.Errorf("latencyMs is outside the bounded audit range")
	}
	return nil
}

func validateBoundedText(name, value string, maxLength int, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be trimmed", name)
	}
	if len(value) > maxLength {
		return fmt.Errorf("%s exceeds maximum length", name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}
