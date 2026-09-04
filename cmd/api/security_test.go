package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
	"github.com/abchw517/kube-aiops/internal/security"
)

func TestBuildHandlerProductionFailsClosedWithoutTrustedIdentity(t *testing.T) {
	_, err := buildHandler(testLogger(), testBackend{}, time.Second, security.ModeProduction, defaultProductionBundle(testLogger()))
	var validationErr *security.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	if validationErr.Component != "authenticator" {
		t.Fatalf("component = %q, want authenticator", validationErr.Component)
	}
}

func TestBuildHandlerDevelopmentRequiresExplicitModeAndPreservesHealth(t *testing.T) {
	handler, err := buildHandler(testLogger(), testBackend{}, time.Second, security.ModeDevelopment, security.Bundle{})
	if err != nil {
		t.Fatalf("buildHandler() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestBuildHandlerProductionAcceptsOnlyCompleteBundle(t *testing.T) {
	bundle := security.Bundle{
		Authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
			return identity.Principal{Subject: "test-user", Provider: "test"}, nil
		}),
		Authorizer: authorization.AuthorizerFunc(func(context.Context, authorization.DecisionRequest) (authorization.Decision, error) {
			return authorization.Decision{Allowed: true}, nil
		}),
		AuditSink: internalaudit.SinkFunc(func(context.Context, internalaudit.Event) error { return nil }),
		Sanitizer: sanitizer.Default(),
	}
	handler, err := buildHandler(testLogger(), testBackend{}, time.Second, security.ModeProduction, bundle)
	if err != nil {
		t.Fatalf("buildHandler() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type testBackend struct{}

func (testBackend) Ready(context.Context) error { return nil }
func (testBackend) Clusters() []kubernetes.Cluster {
	return []kubernetes.Cluster{{ID: "local", Name: "local", Status: "ready"}}
}
func (testBackend) ListNamespaces(context.Context) ([]kubernetes.Namespace, error) {
	return nil, nil
}
func (testBackend) GetResource(context.Context, string, string, string) (kubernetes.ResourceDetail, error) {
	return kubernetes.ResourceDetail{}, nil
}
func (testBackend) ListFindings(context.Context, finding.Query) (finding.Page, error) {
	return finding.Page{}, nil
}
func (testBackend) GetFinding(context.Context, string) (finding.Finding, error) {
	return finding.Finding{}, nil
}
func (testBackend) SummarizeFindings(context.Context, finding.Filter) (finding.Summary, error) {
	return finding.Summary{}, nil
}
