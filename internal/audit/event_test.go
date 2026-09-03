package audit

import (
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
)

func validEvent() Event {
	return Event{
		Timestamp:         time.Now().UTC(),
		RequestID:         "req_123",
		CorrelationID:     "corr_123",
		RoutePattern:      "GET /api/v1/findings",
		Capability:        authorization.CapabilityFindingsList,
		PrincipalSubject:  "user:alice",
		PrincipalProvider: "test",
		Cluster:           "local",
		Namespace:         "dev",
		Outcome:           OutcomeSuccess,
		HTTPStatus:        200,
		LatencyMS:         12,
	}
}

func TestEventValidateAcceptsBoundedEvent(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
}

func TestEventValidateRejectsUnsafeFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Event)
	}{
		{"request control", func(e *Event) { e.RequestID = "req\nsecret" }},
		{"oversized route", func(e *Event) { e.RoutePattern = strings.Repeat("x", maxRoutePatternLength+1) }},
		{"partial principal", func(e *Event) { e.PrincipalProvider = "" }},
		{"namespace without cluster", func(e *Event) { e.Cluster = "" }},
		{"unknown outcome", func(e *Event) { e.Outcome = Outcome("raw-provider-error") }},
		{"bad status", func(e *Event) { e.HTTPStatus = 700 }},
		{"bad latency", func(e *Event) { e.LatencyMS = maxLatencyMS + 1 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := validEvent()
			tc.mutate(&event)
			if err := event.Validate(); err == nil {
				t.Fatal("unsafe event unexpectedly validated")
			}
		})
	}
}

func TestEventAllowsUnauthenticatedPrincipalAndScopeOmission(t *testing.T) {
	event := validEvent()
	event.PrincipalSubject = ""
	event.PrincipalProvider = ""
	event.Cluster = ""
	event.Namespace = ""
	event.Outcome = OutcomeUnauthenticated
	event.HTTPStatus = 401
	if err := event.Validate(); err != nil {
		t.Fatalf("unauthenticated event rejected: %v", err)
	}
}
