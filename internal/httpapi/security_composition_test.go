package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
)

func TestIntegratedSecurityPipelineSanitizesAndAuditsAllowedRequest(t *testing.T) {
	unsafe := "pipeline-secret-value"
	backend := fakeBackend{findingPage: finding.Page{
		Items: []finding.Finding{{
			ID:        "finding-1",
			Cluster:   "local",
			Namespace: "prod",
			Severity:  finding.SeverityWarning,
			Problem:   "Authorization: Bearer " + unsafe,
			Details:   `<script>window.__shouldNotRun = true</script> diagnostic`,
			Source:    "k8sgpt",
		}},
		Pagination: finding.Pagination{Limit: 50},
	}}

	var events []internalaudit.Event
	handler := NewHandlerWithOptions(
		authorizationTestLogger(),
		backend,
		time.Second,
		HandlerOptions{
			Authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
				return identity.Principal{Subject: "alice", Provider: "test-provider", Groups: []string{"sre"}}, nil
			}),
			Authorizer: authorization.AuthorizerFunc(func(_ context.Context, request authorization.DecisionRequest) (authorization.Decision, error) {
				if request.Capability != authorization.CapabilityFindingsList {
					t.Fatalf("capability = %q", request.Capability)
				}
				if request.Scope != authorization.ClusterScope("local") {
					t.Fatalf("scope = %+v, want local cluster scope", request.Scope)
				}
				return authorization.Decision{Allowed: true}, nil
			}),
			AuditSink: internalaudit.SinkFunc(func(_ context.Context, event internalaudit.Event) error {
				events = append(events, event)
				return nil
			}),
			Sanitizer: sanitizer.Default(),
		},
	)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/findings?cluster=local", nil)
	request.Header.Set("Authorization", "Bearer request-value-not-for-audit")
	request.Header.Set("Cookie", "session=not-for-audit")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, body)
	}
	if strings.Contains(body, unsafe) || strings.Contains(strings.ToLower(body), "<script") {
		t.Fatalf("unsafe content crossed response boundary: %s", body)
	}
	if !strings.Contains(body, "[REDACTED:credential]") || !strings.Contains(body, "[REDACTED:active-content]") {
		t.Fatalf("sanitizer markers missing: %s", body)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Outcome != internalaudit.OutcomeSuccess || event.HTTPStatus != http.StatusOK {
		t.Fatalf("audit outcome/status = %q/%d", event.Outcome, event.HTTPStatus)
	}
	if event.PrincipalSubject != "alice" || event.PrincipalProvider != "test-provider" {
		t.Fatalf("audit principal = %q/%q", event.PrincipalSubject, event.PrincipalProvider)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal audit event: %v", err)
	}
	for _, forbidden := range []string{"request-value-not-for-audit", "session=not-for-audit", unsafe, "finding-1"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, encoded)
		}
	}
}
