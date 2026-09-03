package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

func authorizationTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func authenticatedTestPrincipal() identity.Principal {
	return identity.Principal{Subject: "user:alice", Provider: "test", Groups: []string{"sre"}}
}

func authenticatedTestAuthenticator() identity.Authenticator {
	return identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		return authenticatedTestPrincipal(), nil
	})
}

func newSecuredTestHandler(backend fakeBackend, authorizer authorization.Authorizer) http.Handler {
	return NewHandlerWithOptions(
		authorizationTestLogger(),
		backend,
		time.Second,
		HandlerOptions{Authenticator: authenticatedTestAuthenticator(), Authorizer: authorizer},
	)
}

func TestProtectedRouteCapabilityMappingIsExhaustive(t *testing.T) {
	server := &Server{}
	routes := server.protectedRoutes()
	expected := map[string]authorization.Capability{
		"GET /api/v1/clusters":                                               authorization.CapabilityClustersList,
		"GET /api/v1/clusters/{cluster}/namespaces":                          authorization.CapabilityNamespacesList,
		"GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}": authorization.CapabilityResourcesRead,
		"GET /api/v1/findings":                                               authorization.CapabilityFindingsList,
		"GET /api/v1/findings/summary":                                       authorization.CapabilityFindingsSummary,
		"GET /api/v1/findings/{id}":                                          authorization.CapabilityFindingsRead,
	}
	if len(routes) != len(expected) {
		t.Fatalf("protected route count=%d, want %d", len(routes), len(expected))
	}
	for _, route := range routes {
		capability, ok := expected[route.pattern]
		if !ok {
			t.Fatalf("unexpected protected route %q", route.pattern)
		}
		if route.capability != capability {
			t.Fatalf("route %q capability=%q, want %q", route.pattern, route.capability, capability)
		}
		delete(expected, route.pattern)
	}
	if len(expected) != 0 {
		t.Fatalf("missing protected routes: %#v", expected)
	}
}

func TestAuthorizationRouteAllowMatrix(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		capability authorization.Capability
		scope      authorization.Scope
		backend    fakeBackend
	}{
		{
			name:       "clusters",
			path:       "/api/v1/clusters",
			capability: authorization.CapabilityClustersList,
			scope:      authorization.GlobalScope(),
		},
		{
			name:       "namespaces",
			path:       "/api/v1/clusters/local/namespaces",
			capability: authorization.CapabilityNamespacesList,
			scope:      authorization.ClusterScope("local"),
		},
		{
			name:       "resource",
			path:       "/api/v1/clusters/local/resources/pods/dev/demo",
			capability: authorization.CapabilityResourcesRead,
			scope:      authorization.NamespaceScope("local", "dev"),
			backend: fakeBackend{resource: kubernetes.ResourceDetail{
				APIVersion: "v1",
				Kind:       "Pod",
				Namespace:  "dev",
				Name:       "demo",
			}},
		},
		{
			name:       "findings list",
			path:       "/api/v1/findings?cluster=local&namespace=dev",
			capability: authorization.CapabilityFindingsList,
			scope:      authorization.NamespaceScope("local", "dev"),
		},
		{
			name:       "findings summary",
			path:       "/api/v1/findings/summary?namespace=dev",
			capability: authorization.CapabilityFindingsSummary,
			scope:      authorization.NamespaceScope("local", "dev"),
		},
		{
			name:       "finding detail",
			path:       "/api/v1/findings/finding-1",
			capability: authorization.CapabilityFindingsRead,
			scope:      authorization.NamespaceScope("local", "prod"),
			backend: fakeBackend{findingItem: finding.Finding{
				ID:        "finding-1",
				Cluster:   "local",
				Namespace: "prod",
				Severity:  finding.SeverityWarning,
				Source:    "k8sgpt",
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			authorizer := authorization.AuthorizerFunc(func(_ context.Context, request authorization.DecisionRequest) (authorization.Decision, error) {
				calls++
				if request.Principal.Subject != "user:alice" {
					t.Fatalf("unexpected principal: %#v", request.Principal)
				}
				if request.Capability != tc.capability || request.Scope != tc.scope {
					t.Fatalf("authorization request=%+v, want capability=%q scope=%+v", request, tc.capability, tc.scope)
				}
				return authorization.Decision{Allowed: true}, nil
			})

			recorder := httptest.NewRecorder()
			newSecuredTestHandler(tc.backend, authorizer).ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, tc.path, nil),
			)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if calls != 1 {
				t.Fatalf("authorizer calls=%d, want 1", calls)
			}
		})
	}
}

func TestAuthorizationDenyReturnsStableForbidden(t *testing.T) {
	authorizer := authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
		return authorization.Decision{Allowed: false}, nil
	})
	recorder := httptest.NewRecorder()
	newSecuredTestHandler(fakeBackend{}, authorizer).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/findings?namespace=prod", nil),
	)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "AUTHORIZATION_DENIED") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
}

func TestAuthenticationRunsBeforeAuthorization(t *testing.T) {
	authorizerCalls := 0
	authorizer := authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
		authorizerCalls++
		return authorization.Decision{Allowed: true}, nil
	})
	authenticator := identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		return identity.Principal{}, identity.ErrUnauthenticated
	})
	handler := NewHandlerWithOptions(
		authorizationTestLogger(),
		fakeBackend{},
		time.Second,
		HandlerOptions{Authenticator: authenticator, Authorizer: authorizer},
	)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizerCalls != 0 {
		t.Fatalf("authorizer must not run before successful authentication, calls=%d", authorizerCalls)
	}
}

func TestAuthenticatedRequestWithoutAuthorizerFailsClosed(t *testing.T) {
	handler := NewHandlerWithOptions(
		authorizationTestLogger(),
		fakeBackend{},
		time.Second,
		HandlerOptions{Authenticator: authenticatedTestAuthenticator()},
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "AUTHORIZATION_UNAVAILABLE") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthorizerErrorFailsClosed(t *testing.T) {
	authorizer := authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
		return authorization.Decision{}, errors.New("policy backend unavailable")
	})
	recorder := httptest.NewRecorder()
	newSecuredTestHandler(fakeBackend{}, authorizer).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil),
	)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "AUTHORIZATION_UNAVAILABLE") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFindingDetailDeniedAfterScopeResolution(t *testing.T) {
	backend := fakeBackend{findingItem: finding.Finding{
		ID:        "finding-prod",
		Cluster:   "local",
		Namespace: "prod",
		Severity:  finding.SeverityCritical,
		Source:    "k8sgpt",
	}}
	authorizer := authorization.AuthorizerFunc(func(_ context.Context, request authorization.DecisionRequest) (authorization.Decision, error) {
		if request.Capability != authorization.CapabilityFindingsRead {
			t.Fatalf("unexpected capability %q", request.Capability)
		}
		if request.Scope != authorization.NamespaceScope("local", "prod") {
			t.Fatalf("finding scope=%+v, want local/prod", request.Scope)
		}
		return authorization.Decision{Allowed: false}, nil
	})

	recorder := httptest.NewRecorder()
	newSecuredTestHandler(backend, authorizer).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-prod", nil),
	)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "finding-prod") || strings.Contains(recorder.Body.String(), "prod") {
		t.Fatalf("forbidden response leaked finding scope: %s", recorder.Body.String())
	}
}

func TestFindingDetailNotFoundDoesNotInvokeAuthorizer(t *testing.T) {
	calls := 0
	authorizer := authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
		calls++
		return authorization.Decision{Allowed: true}, nil
	})
	backend := fakeBackend{findingGetErr: &kubernetes.APIError{StatusCode: http.StatusNotFound}}
	recorder := httptest.NewRecorder()
	newSecuredTestHandler(backend, authorizer).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/findings/missing", nil),
	)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "FINDING_NOT_FOUND") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if calls != 0 {
		t.Fatalf("non-existent finding must not fabricate a scope decision, calls=%d", calls)
	}
}
