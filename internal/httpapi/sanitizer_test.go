package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
)

func TestFindingListSanitizesDiagnosticTextBeforeResponse(t *testing.T) {
	secret := "very-secret-bearer-value"
	backend := fakeBackend{findingPage: finding.Page{
		Items: []finding.Finding{{
			ID:       "finding-1",
			Cluster:  "local",
			Severity: finding.SeverityWarning,
			Problem:  "Authorization: Bearer " + secret,
			Details:  `<script>alert(1)</script> normal diagnostic text`,
			Source:   "k8sgpt",
		}},
		Pagination: finding.Pagination{Limit: 1},
	}}

	recorder := httptest.NewRecorder()
	newTestHandler(backend).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings?limit=1", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
	if strings.Contains(body, secret) || strings.Contains(strings.ToLower(body), "<script") {
		t.Fatalf("unsafe diagnostic content crossed response boundary: %s", body)
	}
	if !strings.Contains(body, "[REDACTED:credential]") || !strings.Contains(body, "[REDACTED:active-content]") {
		t.Fatalf("expected sanitizer markers, body=%s", body)
	}
}

func TestResourceSanitizationFailureReturnsStableGenericError(t *testing.T) {
	backend := fakeBackend{resource: kubernetes.ResourceDetail{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  "default",
		Name:       "demo\nunsafe-value",
	}}

	recorder := httptest.NewRecorder()
	newTestHandler(backend).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/clusters/local/resources/pods/default/demo", nil),
	)
	body := recorder.Body.String()
	if recorder.Code != http.StatusBadGateway || !strings.Contains(body, "RESPONSE_SANITIZATION_FAILED") {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
	if strings.Contains(body, "unsafe-value") {
		t.Fatalf("sanitizer failure leaked unsafe field: %s", body)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
}

func TestFindingDetailAuthorizesRealScopeBeforeSanitization(t *testing.T) {
	backend := fakeBackend{findingItem: finding.Finding{
		ID:        "finding-prod",
		Cluster:   "local",
		Namespace: "prod",
		Severity:  finding.SeverityWarning,
		Problem:   "token=credential-value",
		Source:    "k8sgpt",
	}}
	spy := &sanitizerSpy{}
	authorizer := authorization.AuthorizerFunc(func(_ context.Context, request authorization.DecisionRequest) (authorization.Decision, error) {
		if request.Scope != authorization.NamespaceScope("local", "prod") {
			t.Fatalf("scope=%+v, want local/prod", request.Scope)
		}
		return authorization.Decision{Allowed: false}, nil
	})
	handler := NewHandlerWithOptions(
		authorizationTestLogger(),
		backend,
		time.Second,
		HandlerOptions{
			Authenticator: authenticatedTestAuthenticator(),
			Authorizer:    authorizer,
			Sanitizer:     spy,
		},
	)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-prod", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if spy.findingCalls != 0 {
		t.Fatalf("sanitizer ran before authorization decision, calls=%d", spy.findingCalls)
	}
}

func TestSanitizationFailureLogNeverContainsUnsafePayload(t *testing.T) {
	unsafe := "do-not-log-this-credential"
	backend := fakeBackend{findingPage: finding.Page{
		Items: []finding.Finding{{
			ID:       "bad\nidentifier",
			Cluster:  "local",
			Severity: finding.SeverityWarning,
			Problem:  "password=" + unsafe,
			Source:   "k8sgpt",
		}},
		Pagination: finding.Pagination{Limit: 1},
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := NewHandler(logger, backend, time.Second)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings?limit=1", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(logs.String(), unsafe) || strings.Contains(logs.String(), "bad\\nidentifier") {
		t.Fatalf("unsafe payload leaked to log: %s", logs.String())
	}
	for _, expected := range []string{"reason=sanitizer_blocked", "capability=findings:list"} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("missing safe log field %q in %s", expected, logs.String())
		}
	}
}

type sanitizerSpy struct {
	findingCalls int
}

func (s *sanitizerSpy) Finding(item finding.Finding) (finding.Finding, error) {
	s.findingCalls++
	return item, nil
}

func (s *sanitizerSpy) FindingPage(page finding.Page) (finding.Page, error) {
	return page, nil
}

func (s *sanitizerSpy) FindingSummary(summary finding.Summary) (finding.Summary, error) {
	return summary, nil
}

func (s *sanitizerSpy) Resource(resource kubernetes.ResourceDetail) (kubernetes.ResourceDetail, error) {
	return resource, nil
}

var _ sanitizer.Sanitizer = (*sanitizerSpy)(nil)
