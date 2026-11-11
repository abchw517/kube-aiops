package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/config"
	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/prometheus"
	"github.com/abchw517/kube-aiops/internal/security"
)

func TestBuildHandlerInjectsEventsPipeline(t *testing.T) {
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
				{Source: correlation.SourceVictoriaLogs, State: correlation.SourceDisabled},
				{Source: correlation.SourceAlertmanager, State: correlation.SourceDisabled},
			},
			Signals: []correlation.CorrelationSignal{{
				ID: "event-1", Source: correlation.SourceKubernetesEvents, Type: correlation.SignalTypeEvent,
				Severity: finding.SeverityWarning, FirstSeen: "2026-09-07T01:59:00Z", LastSeen: "2026-09-07T01:59:00Z",
				Count: 1, Name: "BackOff", Summary: "BackOff: container restart",
			}},
		}, nil
	})

	handler, err := buildHandler(
		testLogger(),
		correlationCompositionBackend{},
		time.Second,
		security.ModeDevelopment,
		security.Bundle{},
		correlator,
	)
	if err != nil {
		t.Fatalf("buildHandler() error=%v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-1/correlation", nil))
	if recorder.Code != http.StatusOK || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body.String())
	}
}

func TestFindingCorrelationReturnsPrometheusEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anchor, err := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"upstreamLabel":"private-label-value"},"values":[[%d,"2"]]}]}}`, anchor.Unix())
	}))
	defer upstream.Close()
	metricSource, err := prometheus.New(prometheus.Config{URL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	correlator, err := correlation.NewService(correlation.ServiceOptions{Prometheus: metricSource})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := buildHandler(testLogger(), correlationCompositionBackend{}, time.Second, security.ModeDevelopment, security.Bundle{}, correlator)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-1/correlation", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("correlation API returned %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-label-value") {
		t.Fatal("upstream labels crossed the API boundary")
	}
	var bundle correlation.CorrelationBundle
	if err := json.Unmarshal(response.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Sources) != correlation.SourceCount || bundle.Sources[1].Source != correlation.SourcePrometheus ||
		bundle.Sources[1].State != correlation.SourceAvailable || len(bundle.Signals) != 5 {
		t.Fatalf("unexpected Prometheus evidence: %#v", bundle)
	}
}

func TestFindingCorrelationUsesConfiguredLogAndAlertSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/logs/select/logsql/stats_query_range":
			if !strings.Contains(r.URL.Query().Get("query"), `cluster:="local-fixture"`) {
				t.Error("VictoriaLogs cluster scope missing")
			}
			bucket := time.Date(2026, 9, 7, 1, 59, 0, 0, time.UTC)
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[
				{"metric":{"__name__":"panic_fatal","raw":"fixture-private-log"},"values":[[%d,"2"]]}]}}`, bucket.Unix())
		case "/am/api/v2/alerts":
			if len(r.URL.Query()["filter"]) != 3 {
				t.Error("Alertmanager resource scope missing")
			}
			fmt.Fprint(w, `[{"labels":{"alertname":"PodFixture","namespace":"prod","pod":"demo","cluster":"local-fixture"},
				"fingerprint":"0123456789abcdef","startsAt":"2026-09-07T01:59:00Z","endsAt":"2026-09-07T02:01:00Z",
				"status":{"state":"active"},"annotations":{"summary":"fixture-private-alert"}}]`)
		default:
			t.Errorf("unexpected upstream route %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, prefix := range []string{"PROMETHEUS", "VICTORIALOGS", "ALERTMANAGER"} {
		t.Setenv(prefix+"_BEARER_TOKEN_FILE", "")
		t.Setenv(prefix+"_CA_FILE", "")
	}
	t.Setenv("VICTORIALOGS_URL", server.URL+"/logs")
	t.Setenv("VICTORIALOGS_CLUSTER_LABEL_VALUE", "local-fixture")
	t.Setenv("ALERTMANAGER_URL", server.URL+"/am")
	t.Setenv("ALERTMANAGER_CLUSTER_LABEL_VALUE", "local-fixture")
	t.Setenv("PROMETHEUS_URL", "")
	t.Setenv("PROMETHEUS_CLUSTER_LABEL_VALUE", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	correlator, err := buildCorrelator(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := buildHandler(testLogger(), correlationCompositionBackend{}, time.Second, security.ModeDevelopment, security.Bundle{}, correlator)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/findings/finding-1/correlation", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "fixture-private-") {
		t.Fatalf("unexpected correlation response %d: %s", response.Code, response.Body.String())
	}
	var bundle correlation.CorrelationBundle
	if err := json.Unmarshal(response.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Signals) != 2 || bundle.Signals[0].Source != correlation.SourceVictoriaLogs ||
		bundle.Signals[0].Count != 2 || bundle.Signals[1].Source != correlation.SourceAlertmanager ||
		bundle.Signals[1].Name != "PodFixture" {
		t.Fatalf("unexpected evidence: %#v", bundle)
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
