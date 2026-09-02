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

	"github.com/abchw517/kube-aiops/internal/identity"
)

func TestRequestMetadataGenerated(t *testing.T) {
	handler := requestMetadataMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFromContext(r.Context()) == "" {
			t.Fatal("expected request ID in context")
		}
		if CorrelationIDFromContext(r.Context()) == "" {
			t.Fatal("expected correlation ID in context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	requestID := recorder.Header().Get(RequestIDHeader)
	correlationID := recorder.Header().Get(CorrelationIDHeader)
	if requestID == "" || correlationID == "" {
		t.Fatalf("expected response IDs, got request=%q correlation=%q", requestID, correlationID)
	}
	if requestID != correlationID {
		t.Fatalf("expected correlation ID to default to request ID, got request=%q correlation=%q", requestID, correlationID)
	}
}

func TestRequestMetadataPreservesValidInboundIDs(t *testing.T) {
	handler := requestMetadataMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(RequestIDHeader, "req-client-123")
	request.Header.Set(CorrelationIDHeader, "corr-change-456")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get(RequestIDHeader); got != "req-client-123" {
		t.Fatalf("unexpected request ID: %q", got)
	}
	if got := recorder.Header().Get(CorrelationIDHeader); got != "corr-change-456" {
		t.Fatalf("unexpected correlation ID: %q", got)
	}
}

func TestRequestMetadataRejectsUnsafeInboundIDs(t *testing.T) {
	handler := requestMetadataMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(RequestIDHeader, "unsafe value")
	request.Header.Set(CorrelationIDHeader, "bad\tvalue")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	requestID := recorder.Header().Get(RequestIDHeader)
	correlationID := recorder.Header().Get(CorrelationIDHeader)
	if requestID == "unsafe value" || requestID == "" {
		t.Fatalf("unsafe request ID was not replaced: %q", requestID)
	}
	if correlationID != requestID {
		t.Fatalf("invalid correlation ID should fall back to request ID, got %q", correlationID)
	}
}

func TestAuthenticationMiddlewareRejectsUnauthenticatedAPIRequest(t *testing.T) {
	authenticator := identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		return identity.Principal{}, identity.ErrUnauthenticated
	})

	handler := NewHandlerWithOptions(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeBackend{},
		time.Second,
		HandlerOptions{Authenticator: authenticator},
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "AUTHENTICATION_REQUIRED") {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	if recorder.Header().Get(RequestIDHeader) == "" || recorder.Header().Get(CorrelationIDHeader) == "" {
		t.Fatal("authentication error must include request and correlation headers")
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}
}

func TestAuthenticationMiddlewareSkipsHealthEndpoints(t *testing.T) {
	calls := 0
	authenticator := identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		calls++
		return identity.Principal{}, identity.ErrUnauthenticated
	})

	handler := NewHandlerWithOptions(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeBackend{},
		time.Second,
		HandlerOptions{Authenticator: authenticator},
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if calls != 0 {
		t.Fatalf("health endpoint should not invoke authenticator, calls=%d", calls)
	}
}

func TestAuthenticationMiddlewareInjectsPrincipal(t *testing.T) {
	principal := identity.Principal{Subject: "user:alice", Provider: "oidc", Groups: []string{"sre"}}
	authenticator := identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		return principal, nil
	})
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	var got identity.Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		got, ok = identity.PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("expected authenticated principal in context")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := requestMetadataMiddleware(server.authenticationMiddleware(authenticator, next))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
	}
	if got.Subject != principal.Subject || got.Provider != principal.Provider {
		t.Fatalf("unexpected principal: %#v", got)
	}
}

func TestAuthenticationMiddlewareRejectsInvalidPrincipal(t *testing.T) {
	authenticator := identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		return identity.Principal{Subject: " alice ", Provider: "oidc"}, nil
	})

	handler := NewHandlerWithOptions(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeBackend{},
		time.Second,
		HandlerOptions{Authenticator: authenticator},
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "AUTHENTICATION_UNAVAILABLE") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthenticationMiddlewareFailsClosedOnProviderError(t *testing.T) {
	authenticator := identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
		return identity.Principal{}, errors.New("provider timeout")
	})

	handler := NewHandlerWithOptions(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeBackend{},
		time.Second,
		HandlerOptions{Authenticator: authenticator},
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "AUTHENTICATION_UNAVAILABLE") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestForbiddenResponseContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeForbidden(recorder)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "AUTHORIZATION_DENIED") {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}
}
