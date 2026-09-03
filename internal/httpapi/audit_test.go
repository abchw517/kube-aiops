package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

type captureAuditSink struct {
	events []internalaudit.Event
	err    error
}

func (s *captureAuditSink) Record(_ context.Context, event internalaudit.Event) error {
	s.events = append(s.events, event)
	return s.err
}

func auditedHandler(
	logger *slog.Logger,
	backend fakeBackend,
	authenticator identity.Authenticator,
	authorizer authorization.Authorizer,
	sink internalaudit.Sink,
) http.Handler {
	return NewHandlerWithOptions(logger, backend, time.Second, HandlerOptions{
		Authenticator: authenticator,
		Authorizer:    authorizer,
		AuditSink:     sink,
	})
}

func allowAllAuthorizer() authorization.Authorizer {
	return authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
		return authorization.Decision{Allowed: true}, nil
	})
}

func TestAuditSuccessCapturesOnlyBoundedSecurityContext(t *testing.T) {
	sink := &captureAuditSink{}
	principal := identity.Principal{
		Subject:     "user:alice",
		Provider:    "test",
		DisplayName: "display-name-secret",
		Groups:      []string{"group-secret"},
	}
	authenticator := identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		return principal, nil
	})
	backend := fakeBackend{findingPage: finding.Page{
		Items: []finding.Finding{{ID: "opaque-finding-secret", Cluster: "local", Severity: finding.SeverityWarning, Source: "k8sgpt"}},
	}}
	handler := auditedHandler(authorizationTestLogger(), backend, authenticator, allowAllAuthorizer(), sink)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/findings?cluster=local&namespace=dev&problem=query-secret", nil)
	request.Header.Set("Authorization", "Bearer bearer-secret")
	request.Header.Set("Cookie", "session=session-secret")
	request.Header.Set(RequestIDHeader, "req-audit-1")
	request.Header.Set(CorrelationIDHeader, "corr-audit-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(sink.events) != 1 {
		t.Fatalf("events=%d, want 1", len(sink.events))
	}
	event := sink.events[0]
	if event.RequestID != "req-audit-1" || event.CorrelationID != "corr-audit-1" {
		t.Fatalf("unexpected request metadata: %+v", event)
	}
	if event.RoutePattern != "GET /api/v1/findings" || event.Capability != authorization.CapabilityFindingsList {
		t.Fatalf("unexpected route audit metadata: %+v", event)
	}
	if event.PrincipalSubject != "user:alice" || event.PrincipalProvider != "test" {
		t.Fatalf("unexpected principal audit metadata: %+v", event)
	}
	if event.Cluster != "local" || event.Namespace != "dev" {
		t.Fatalf("unexpected scope: %+v", event)
	}
	if event.Outcome != internalaudit.OutcomeSuccess || event.HTTPStatus != http.StatusOK || event.Timestamp.IsZero() || event.LatencyMS < 0 {
		t.Fatalf("unexpected result metadata: %+v", event)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"bearer-secret",
		"session-secret",
		"query-secret",
		"opaque-finding-secret",
		"display-name-secret",
		"group-secret",
		"Authorization",
		"Cookie",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("audit event leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAuditSecurityOutcomes(t *testing.T) {
	cases := []struct {
		name          string
		authenticator identity.Authenticator
		authorizer    authorization.Authorizer
		wantStatus    int
		wantOutcome   internalaudit.Outcome
	}{
		{
			name: "unauthenticated",
			authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
				return identity.Principal{}, identity.ErrUnauthenticated
			}),
			authorizer:  allowAllAuthorizer(),
			wantStatus:  http.StatusUnauthorized,
			wantOutcome: internalaudit.OutcomeUnauthenticated,
		},
		{
			name: "authentication unavailable",
			authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
				return identity.Principal{}, errors.New("provider raw secret")
			}),
			authorizer:  allowAllAuthorizer(),
			wantStatus:  http.StatusServiceUnavailable,
			wantOutcome: internalaudit.OutcomeSecurityUnavailable,
		},
		{
			name:          "authorization denied",
			authenticator: authenticatedTestAuthenticator(),
			authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
				return authorization.Decision{Allowed: false}, nil
			}),
			wantStatus:  http.StatusForbidden,
			wantOutcome: internalaudit.OutcomeDenied,
		},
		{
			name:          "authorization unavailable",
			authenticator: authenticatedTestAuthenticator(),
			authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
				return authorization.Decision{}, errors.New("policy raw secret")
			}),
			wantStatus:  http.StatusServiceUnavailable,
			wantOutcome: internalaudit.OutcomeSecurityUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &captureAuditSink{}
			handler := auditedHandler(authorizationTestLogger(), fakeBackend{}, tc.authenticator, tc.authorizer, sink)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/local/namespaces", nil))
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if len(sink.events) != 1 {
				t.Fatalf("events=%d, want 1", len(sink.events))
			}
			event := sink.events[0]
			if event.Outcome != tc.wantOutcome || event.HTTPStatus != tc.wantStatus {
				t.Fatalf("event=%+v", event)
			}
			if event.RoutePattern != "GET /api/v1/clusters/{cluster}/namespaces" || event.Capability != authorization.CapabilityNamespacesList {
				t.Fatalf("unexpected canonical route metadata: %+v", event)
			}
		})
	}
}

func TestAuditFindingDetailUsesResolvedScopeWithoutOpaqueID(t *testing.T) {
	sink := &captureAuditSink{}
	backend := fakeBackend{findingItem: finding.Finding{
		ID:        "opaque-finding-id-secret",
		Cluster:   "local",
		Namespace: "prod",
		Severity:  finding.SeverityCritical,
		Source:    "k8sgpt",
	}}
	handler := auditedHandler(authorizationTestLogger(), backend, authenticatedTestAuthenticator(), allowAllAuthorizer(), sink)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/opaque-finding-id-secret", nil))

	if recorder.Code != http.StatusOK || len(sink.events) != 1 {
		t.Fatalf("status=%d events=%d body=%s", recorder.Code, len(sink.events), recorder.Body.String())
	}
	event := sink.events[0]
	if event.RoutePattern != "GET /api/v1/findings/{id}" || event.Cluster != "local" || event.Namespace != "prod" {
		t.Fatalf("unexpected finding audit event: %+v", event)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "opaque-finding-id-secret") {
		t.Fatalf("finding ID leaked into audit event: %s", encoded)
	}
}

func TestAuditFindingNotFoundDoesNotFabricateScope(t *testing.T) {
	sink := &captureAuditSink{}
	backend := fakeBackend{findingGetErr: &kubernetes.APIError{StatusCode: http.StatusNotFound}}
	handler := auditedHandler(authorizationTestLogger(), backend, authenticatedTestAuthenticator(), allowAllAuthorizer(), sink)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/missing-secret-id", nil))

	if recorder.Code != http.StatusNotFound || len(sink.events) != 1 {
		t.Fatalf("status=%d events=%d body=%s", recorder.Code, len(sink.events), recorder.Body.String())
	}
	event := sink.events[0]
	if event.Outcome != internalaudit.OutcomeNotFound || event.Cluster != "" || event.Namespace != "" {
		t.Fatalf("unexpected not-found audit event: %+v", event)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "missing-secret-id") {
		t.Fatalf("missing finding ID leaked into audit event: %s", encoded)
	}
}

func TestAuditHandlerOutcomeMapping(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		backend     fakeBackend
		wantStatus  int
		wantOutcome internalaudit.Outcome
	}{
		{
			name:        "invalid request",
			path:        "/api/v1/findings?limit=abc",
			wantStatus:  http.StatusBadRequest,
			wantOutcome: internalaudit.OutcomeInvalidRequest,
		},
		{
			name:        "backend error",
			path:        "/api/v1/findings",
			backend:     fakeBackend{findingListErr: errors.New("backend raw secret")},
			wantStatus:  http.StatusBadGateway,
			wantOutcome: internalaudit.OutcomeBackendError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &captureAuditSink{}
			handler := auditedHandler(authorizationTestLogger(), tc.backend, authenticatedTestAuthenticator(), allowAllAuthorizer(), sink)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != tc.wantStatus || len(sink.events) != 1 {
				t.Fatalf("status=%d events=%d body=%s", recorder.Code, len(sink.events), recorder.Body.String())
			}
			if sink.events[0].Outcome != tc.wantOutcome {
				t.Fatalf("event=%+v", sink.events[0])
			}
		})
	}
}

func TestAuditSinkFailureDoesNotRewriteSecurityDecisionOrLeakError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	sink := &captureAuditSink{err: errors.New("sink-super-secret-error")}
	authorizer := authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
		return authorization.Decision{Allowed: false}, nil
	})
	handler := auditedHandler(logger, fakeBackend{}, authenticatedTestAuthenticator(), authorizer, sink)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("audit sink failure changed security response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "sink-super-secret-error") || strings.Contains(logs.String(), "sink-super-secret-error") {
		t.Fatalf("raw sink error leaked response=%s logs=%s", recorder.Body.String(), logs.String())
	}
	if !strings.Contains(logs.String(), "reason=sink_error") {
		t.Fatalf("safe audit failure signal missing: %s", logs.String())
	}
}

func TestAuditSkipsHealthEndpoints(t *testing.T) {
	sink := &captureAuditSink{}
	handler := auditedHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeBackend{},
		authenticatedTestAuthenticator(),
		allowAllAuthorizer(),
		sink,
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(sink.events) != 0 {
		t.Fatalf("health endpoint generated user audit events: %+v", sink.events)
	}
}
