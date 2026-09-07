package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

func TestFindingCorrelationAllSourcesUnavailableReturnsStable503(t *testing.T) {
	item := finding.Finding{ID: "finding-1", Cluster: "local", Severity: finding.SeverityWarning, Source: "k8sgpt"}
	handler := NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: item}, time.Second, HandlerOptions{
		Authenticator: authenticatedTestAuthenticator(),
		Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
			return authorization.Decision{Allowed: true}, nil
		}),
		Correlator: correlation.CorrelatorFunc(func(context.Context, correlation.Request) (correlation.CorrelationBundle, error) {
			return correlation.CorrelationBundle{}, correlation.ErrUnavailable
		}),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-1/correlation", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "CORRELATION_UNAVAILABLE") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFindingCorrelationSanitizesSummaryAndHasNoForbiddenRawFields(t *testing.T) {
	item := finding.Finding{ID: "finding-1", Cluster: "local", Namespace: "prod", Severity: finding.SeverityWarning, Source: "k8sgpt", CreatedAt: "2026-09-07T02:00:00Z"}
	secret := "correlation-secret-value"
	correlator := correlation.CorrelatorFunc(func(_ context.Context, request correlation.Request) (correlation.CorrelationBundle, error) {
		bundle := validHTTPBundle(request)
		bundle.Sources[2].State = correlation.SourceAvailable
		bundle.Signals = []correlation.CorrelationSignal{{
			ID: "loki-fingerprint-1", Source: correlation.SourceLoki, Type: correlation.SignalTypeLog,
			Fingerprint: "sha256:abc123", Count: 3,
			Summary: "token=" + secret + " <script>alert(1)</script>",
		}}
		return bundle, nil
	})
	handler := NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: item}, time.Second, HandlerOptions{
		Authenticator: authenticatedTestAuthenticator(),
		Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
			return authorization.Decision{Allowed: true}, nil
		}),
		Correlator: correlator,
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-1/correlation", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
	if strings.Contains(body, secret) || strings.Contains(strings.ToLower(body), "<script") {
		t.Fatalf("unsafe signal content crossed response boundary: %s", body)
	}
	for _, forbidden := range []string{"promql", "logql", "upstreamurl", "rawlog", "rawline", "rawobject", "rawresult"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("forbidden field %q present: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "[REDACTED:credential]") || !strings.Contains(body, "[REDACTED:active-content]") {
		t.Fatalf("sanitizer markers missing: %s", body)
	}
}

func TestFindingCorrelationAuditRecordsResolvedScopeAndIDs(t *testing.T) {
	item := finding.Finding{ID: "finding-audit", Cluster: "local", Namespace: "audit-ns", Severity: finding.SeverityWarning, Source: "k8sgpt", CreatedAt: "2026-09-07T02:00:00Z"}
	var events []internalaudit.Event
	handler := NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: item}, time.Second, HandlerOptions{
		Authenticator: authenticatedTestAuthenticator(),
		Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
			return authorization.Decision{Allowed: true}, nil
		}),
		AuditSink: internalaudit.SinkFunc(func(_ context.Context, event internalaudit.Event) error {
			events = append(events, event)
			return nil
		}),
		Correlator: correlation.Disabled(),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-audit/correlation", nil)
	request.Header.Set(RequestIDHeader, "req-audit-correlation")
	request.Header.Set(CorrelationIDHeader, "corr-audit-correlation")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(events) != 1 {
		t.Fatalf("status=%d auditEvents=%d body=%s", recorder.Code, len(events), recorder.Body.String())
	}
	event := events[0]
	if event.RoutePattern != "GET /api/v1/findings/{id}/correlation" || event.Capability != authorization.CapabilityCorrelationsRead || event.Cluster != "local" || event.Namespace != "audit-ns" || event.Outcome != internalaudit.OutcomeSuccess || event.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected audit event: %#v", event)
	}
	if event.RequestID != "req-audit-correlation" || event.CorrelationID != "corr-audit-correlation" {
		t.Fatalf("audit IDs not retained: %#v", event)
	}
}

func TestFindingCorrelationUnknownServiceErrorIsGeneric502(t *testing.T) {
	item := finding.Finding{ID: "finding-1", Cluster: "local", Severity: finding.SeverityWarning, Source: "k8sgpt"}
	handler := NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: item}, time.Second, HandlerOptions{
		Authenticator: authenticatedTestAuthenticator(),
		Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
			return authorization.Decision{Allowed: true}, nil
		}),
		Correlator: correlation.CorrelatorFunc(func(context.Context, correlation.Request) (correlation.CorrelationBundle, error) {
			return correlation.CorrelationBundle{}, errors.New("upstream detail must not escape")
		}),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-1/correlation", nil))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "CORRELATION_READ_FAILED") || strings.Contains(recorder.Body.String(), "upstream detail") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
