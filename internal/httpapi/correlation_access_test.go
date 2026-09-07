package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

type correlationSpy struct {
	calls   int
	request correlation.Request
	bundle  correlation.CorrelationBundle
	err     error
}

func (s *correlationSpy) Correlate(_ context.Context, request correlation.Request) (correlation.CorrelationBundle, error) {
	s.calls++
	s.request = request
	if s.bundle.FindingID == "" {
		s.bundle = validHTTPBundle(request)
	}
	return s.bundle, s.err
}

func TestFindingCorrelationAuthenticationAndAuthorization(t *testing.T) {
	item := finding.Finding{
		ID: "opaque-id", Cluster: "local", Namespace: "prod", Severity: finding.SeverityWarning,
		Resource: finding.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "demo"}, Source: "k8sgpt",
		CreatedAt: "2026-09-07T02:00:00Z",
	}

	t.Run("unauthenticated", func(t *testing.T) {
		spy := &correlationSpy{}
		authorizerCalls := 0
		handler := NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: item}, time.Second, HandlerOptions{
			Authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
				return identity.Principal{}, identity.ErrUnauthenticated
			}),
			Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
				authorizerCalls++
				return authorization.Decision{Allowed: true}, nil
			}),
			Correlator: spy,
		})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/opaque-id/correlation", nil))
		if recorder.Code != http.StatusUnauthorized || authorizerCalls != 0 || spy.calls != 0 {
			t.Fatalf("status=%d authorizer=%d correlator=%d body=%s", recorder.Code, authorizerCalls, spy.calls, recorder.Body.String())
		}
	})

	t.Run("authenticated without correlations read", func(t *testing.T) {
		spy := &correlationSpy{}
		authorizer := authorization.AuthorizerFunc(func(_ context.Context, request authorization.DecisionRequest) (authorization.Decision, error) {
			if request.Capability != authorization.CapabilityCorrelationsRead {
				t.Fatalf("capability=%q", request.Capability)
			}
			return authorization.Decision{Allowed: false}, nil
		})
		recorder := httptest.NewRecorder()
		NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: item}, time.Second, HandlerOptions{
			Authenticator: authenticatedTestAuthenticator(), Authorizer: authorizer, Correlator: spy,
		}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/opaque-id/correlation", nil))
		if recorder.Code != http.StatusForbidden || spy.calls != 0 {
			t.Fatalf("status=%d correlator=%d body=%s", recorder.Code, spy.calls, recorder.Body.String())
		}
	})
}

func TestFindingCorrelationUsesResolvedScopeNotOpaqueID(t *testing.T) {
	cases := []struct {
		name      string
		item      finding.Finding
		wantScope authorization.Scope
	}{
		{
			name:      "namespace denied",
			item:      finding.Finding{ID: "opaque-123", Cluster: "local", Namespace: "restricted", Severity: finding.SeverityWarning, Source: "k8sgpt"},
			wantScope: authorization.NamespaceScope("local", "restricted"),
		},
		{
			name:      "cluster denied",
			item:      finding.Finding{ID: "opaque-456", Cluster: "local", Severity: finding.SeverityWarning, Source: "k8sgpt"},
			wantScope: authorization.ClusterScope("local"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &correlationSpy{}
			authorizer := authorization.AuthorizerFunc(func(_ context.Context, request authorization.DecisionRequest) (authorization.Decision, error) {
				if request.Capability != authorization.CapabilityCorrelationsRead || request.Scope != tc.wantScope {
					t.Fatalf("authorization request=%+v want=%+v", request, tc.wantScope)
				}
				return authorization.Decision{Allowed: false}, nil
			})
			recorder := httptest.NewRecorder()
			NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: tc.item}, time.Second, HandlerOptions{
				Authenticator: authenticatedTestAuthenticator(), Authorizer: authorizer, Correlator: spy,
			}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/"+tc.item.ID+"/correlation", nil))
			if recorder.Code != http.StatusForbidden || spy.calls != 0 {
				t.Fatalf("status=%d correlator=%d body=%s", recorder.Code, spy.calls, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), tc.item.ID) || (tc.item.Namespace != "" && strings.Contains(recorder.Body.String(), tc.item.Namespace)) {
				t.Fatalf("forbidden response leaked opaque id/scope: %s", recorder.Body.String())
			}
		})
	}
}

func TestFindingCorrelationAllowedScopeAndRequestIDs(t *testing.T) {
	item := finding.Finding{
		ID: "finding-allowed", Cluster: "local", Namespace: "dev", Severity: finding.SeverityWarning,
		Resource: finding.ResourceRef{APIVersion: "v1", Kind: "Pod", Namespace: "dev", Name: "demo"},
		Source: "k8sgpt", CreatedAt: "2026-09-07T02:00:00Z",
	}
	spy := &correlationSpy{}
	authorizer := authorization.AuthorizerFunc(func(_ context.Context, request authorization.DecisionRequest) (authorization.Decision, error) {
		if request.Capability != authorization.CapabilityCorrelationsRead || request.Scope != authorization.NamespaceScope("local", "dev") {
			t.Fatalf("unexpected authorization request: %+v", request)
		}
		return authorization.Decision{Allowed: true}, nil
	})
	handler := NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{findingItem: item}, time.Second, HandlerOptions{
		Authenticator: authenticatedTestAuthenticator(), Authorizer: authorizer, Correlator: spy,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-allowed/correlation", nil)
	request.Header.Set(RequestIDHeader, "req-phase21")
	request.Header.Set(CorrelationIDHeader, "corr-phase21")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || spy.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, spy.calls, recorder.Body.String())
	}
	if spy.request.FindingID != item.ID || spy.request.Scope.Namespace != "dev" || spy.request.Scope.Resource.Name != "demo" {
		t.Fatalf("correlator received wrong resolved scope: %#v", spy.request)
	}
	if recorder.Header().Get(RequestIDHeader) != "req-phase21" || recorder.Header().Get(CorrelationIDHeader) != "corr-phase21" {
		t.Fatalf("request/correlation IDs not retained: %#v", recorder.Header())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
	}
}

func TestFindingCorrelationMissingFindingDoesNotAuthorizeOrLeakScope(t *testing.T) {
	calls := 0
	spy := &correlationSpy{}
	handler := NewHandlerWithOptions(authorizationTestLogger(), fakeBackend{
		findingGetErr: &kubernetes.APIError{StatusCode: http.StatusNotFound},
	}, time.Second, HandlerOptions{
		Authenticator: authenticatedTestAuthenticator(),
		Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
			calls++
			return authorization.Decision{Allowed: true}, nil
		}),
		Correlator: spy,
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/missing/correlation", nil))
	if recorder.Code != http.StatusNotFound || calls != 0 || spy.calls != 0 || !strings.Contains(recorder.Body.String(), "FINDING_NOT_FOUND") {
		t.Fatalf("status=%d authz=%d correlator=%d body=%s", recorder.Code, calls, spy.calls, recorder.Body.String())
	}
	for _, forbidden := range []string{"namespace", "cluster", "restricted"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("missing response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func validHTTPBundle(request correlation.Request) correlation.CorrelationBundle {
	return correlation.CorrelationBundle{
		FindingID: request.FindingID,
		Scope:     request.Scope,
		Window:    correlation.TimeWindow{Start: "2026-09-07T01:30:00Z", End: "2026-09-07T02:00:00Z"},
		Budget:    correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 50},
		Sources: []correlation.SourceStatus{
			{Source: correlation.SourceKubernetesEvents, State: correlation.SourceDisabled},
			{Source: correlation.SourcePrometheus, State: correlation.SourceDisabled},
			{Source: correlation.SourceLoki, State: correlation.SourceDisabled},
			{Source: correlation.SourceAlertmanager, State: correlation.SourceDisabled},
		},
		Signals: []correlation.CorrelationSignal{},
	}
}
