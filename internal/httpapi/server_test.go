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

	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

type fakeBackend struct {
	readyErr       error
	namespaceErr   error
	resourceErr    error
	findingListErr error
	findingGetErr  error
	findingSumErr  error
	namespaces     []kubernetes.Namespace
	resource       kubernetes.ResourceDetail
	findingPage    finding.Page
	findingItem    finding.Finding
	findingSummary finding.Summary
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
func (f fakeBackend) ListFindings(context.Context, finding.Query) (finding.Page, error) {
	return f.findingPage, f.findingListErr
}
func (f fakeBackend) GetFinding(context.Context, string) (finding.Finding, error) {
	return f.findingItem, f.findingGetErr
}
func (f fakeBackend) SummarizeFindings(context.Context, finding.Filter) (finding.Summary, error) {
	return f.findingSummary, f.findingSumErr
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

func TestFindingList(t *testing.T) {
	backend := fakeBackend{findingPage: finding.Page{
		Items: []finding.Finding{{ID: "finding-1", Cluster: "local", Severity: finding.SeverityWarning, Source: "k8sgpt"}},
		Pagination: finding.Pagination{Limit: 50},
	}}
	recorder := httptest.NewRecorder()
	newTestHandler(backend).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings?cluster=local&limit=50", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "finding-1") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFindingInvalidLimit(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings?limit=abc", nil))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "INVALID_LIMIT") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFindingDetailNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestHandler(fakeBackend{findingGetErr: &kubernetes.APIError{StatusCode: http.StatusNotFound}}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/findings/missing", nil),
	)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "FINDING_NOT_FOUND") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFindingSummary(t *testing.T) {
	backend := fakeBackend{findingSummary: finding.Summary{
		Total: 2,
		BySeverity: map[string]int{finding.SeverityCritical: 0, finding.SeverityWarning: 2, finding.SeverityInfo: 0},
		ByKind: map[string]int{"Pod": 1, "Deployment": 1},
		ByNamespace: map[string]int{"dev": 2},
	}}
	recorder := httptest.NewRecorder()
	newTestHandler(backend).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/summary?namespace=dev", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "\"total\":2") {
		t.Fatalf("unexpected response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
