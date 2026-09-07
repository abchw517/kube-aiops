package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/security"
)

func TestBuildHandlerWithCorrelatorInjectsEventsPipeline(t *testing.T) {
	calls := 0
	correlator := correlation.CorrelatorFunc(func(_ context.Context, request correlation.Request) (correlation.CorrelationBundle, error) {
		calls++
		if request.FindingID != "finding-1" || request.Scope.Namespace != "prod" || request.Scope.Resource.Name != "demo" {
			t.Fatalf("unexpected correlation request: %#v", request)
		}
		return correlation.CorrelationBundle{
			FindingID: request.FindingID,
			Scope:     request.Scope,
			Window:    correlation.TimeWindow{Start: "2026-09-07T01:30:00Z", End: "2026-09-07T02:00:00Z"},
			Budget:    correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 50},
			Sources: []correlation.SourceStatus{
				{Source: correlation.SourceKubernetesEvents, State: correlation.SourceAvailable},
				{Source: correlation.SourcePrometheus, State: correlation.SourceDisabled},
				{Source: correlation.SourceLoki, State: correlation.SourceDisabled},
				{Source: correlation.SourceAlertmanager, State: correlation.SourceDisabled},
			},
			Signals: []correlation.CorrelationSignal{{
				ID: "event-1", Source: correlation.SourceKubernetesEvents, Type: correlation.SignalTypeEvent,
				Severity: finding.SeverityWarning, FirstSeen: "2026-09-07T01:59:00Z", LastSeen: "2026-09-07T01:59:00Z",
				Count: 1, Name: "BackOff", Summary: "BackOff: container restart",
			}},
		}, nil
	})

	handler, err := buildHandlerWithCorrelator(
		testLogger(),
		correlationCompositionBackend{},
		time.Second,
		security.ModeDevelopment,
		security.Bundle{},
		correlator,
	)
	if err != nil {
		t.Fatalf("buildHandlerWithCorrelator() error=%v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-1/correlation", nil))
	if recorder.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body.String())
	}
}

type correlationCompositionBackend struct{}

func (correlationCompositionBackend) Ready(context.Context) error { return nil }
func (correlationCompositionBackend) Clusters() []kubernetes.Cluster {
	return []kubernetes.Cluster{{ID: "local", Name: "local", Status: "ready"}}
}
func (correlationCompositionBackend) ListNamespaces(context.Context) ([]kubernetes.Namespace, error) {
	return nil, nil
}
func (correlationCompositionBackend) GetResource(context.Context, string, string, string) (kubernetes.ResourceDetail, error) {
	return kubernetes.ResourceDetail{}, nil
}
func (correlationCompositionBackend) ListFindings(context.Context, finding.Query) (finding.Page, error) {
	return finding.Page{}, nil
}
func (correlationCompositionBackend) GetFinding(context.Context, string) (finding.Finding, error) {
	return finding.Finding{
		ID: "finding-1", Cluster: "local", Namespace: "prod", Severity: finding.SeverityWarning,
		Resource: finding.ResourceRef{APIVersion: "v1", Kind: "Pod", Namespace: "prod", Name: "demo"},
		Source:   "k8sgpt", CreatedAt: "2026-09-07T02:00:00Z",
	}, nil
}
func (correlationCompositionBackend) SummarizeFindings(context.Context, finding.Filter) (finding.Summary, error) {
	return finding.Summary{}, nil
}
