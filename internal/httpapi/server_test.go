package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

type fakeBackend struct {
	readyErr      error
	namespaceErr  error
	resourceErr   error
	namespaces    []kubernetes.Namespace
	resource      kubernetes.ResourceDetail
}

func (f fakeBackend) Ready(context.Context) error { return f.readyErr }
func (f fakeBackend) Clusters() []kubernetes.Cluster {
	return []kubernetes.Cluster{{ID: "local", Name: "local", Status: "ready"}}
}
func (f fakeBackend) ListNamespaces(context.Context) ([]kubernetes.Namespace, error) {
	return f.namespaces, f.namespaceErr
}
func (f fakeBackend) GetResource(context.Context, string, string, string) (kubernetes.ResourceDetail, error) {
	return f.resource, f.resourceErr
}

func newTestHandler(backend fakeBackend) http.Handler {
	return NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), backend, time.Second)
}

func TestHealthz(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
}

func TestReadyzSuccess(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
}

func TestReadyzFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{readyErr: errors.New("kubernetes unavailable")}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/readyz", nil),
	)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, recorder.Code)
	}
}

func TestClusters(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if body := recorder.Body.String(); body != "{\"items\":[{\"id\":\"local\",\"name\":\"local\",\"status\":\"ready\"}]}\n" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestNamespaces(t *testing.T) {
	backend := fakeBackend{namespaces: []kubernetes.Namespace{{Name: "default"}, {Name: "kube-system"}}}
	recorder := httptest.NewRecorder()
	newTestHandler(backend).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/clusters/local/namespaces", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
}

func TestUnknownCluster(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/clusters/other/namespaces", nil),
	)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}
}

func TestPodResource(t *testing.T) {
	backend := fakeBackend{resource: kubernetes.ResourceDetail{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  "default",
		Name:       "demo",
		Status:     kubernetes.ResourceStatus{Phase: "Running"},
	}}
	recorder := httptest.NewRecorder()
	newTestHandler(backend).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/clusters/local/resources/pods/default/demo", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
}

func TestUnsupportedResourceKind(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/clusters/local/resources/secrets/default/demo", nil),
	)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}
}

func TestResourceNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{resourceErr: &kubernetes.APIError{StatusCode: http.StatusNotFound}}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/clusters/local/resources/deployments/default/missing", nil),
	)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}
}
