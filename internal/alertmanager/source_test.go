package alertmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/observability"
)

func TestAlertsVerifyScopeWindowAndSignalBudget(t *testing.T) {
	query := correlation.Query{
		Scope: correlation.CorrelationScope{Cluster: finding.LocalCluster, Namespace: "payments",
			Resource: finding.ResourceRef{Kind: "Pod", Namespace: "payments", Name: "checkout"}},
		Window: correlation.TimeWindow{Start: "2026-11-11T08:00:00Z", End: "2026-11-11T08:30:00Z"},
		Budget: correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 1000, MaxSignalsPerSource: 10},
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		want := []string{`pod="checkout"`, `namespace="payments"`, `cluster="prod"`}
		if r.Method != http.MethodGet || r.URL.Path != "/am/api/v2/alerts" || !reflect.DeepEqual(r.URL.Query()["filter"], want) {
			t.Errorf("unexpected Alertmanager request: %s %s", r.Method, r.URL)
		}
		fmt.Fprint(w, `[
			{"labels":{"alertname":"OldAlert","pod":"checkout","namespace":"payments","cluster":"prod"},
			 "fingerprint":"1111111111111111","startsAt":"2026-11-11T07:00:00Z","endsAt":"2026-11-11T07:30:00Z","status":{"state":"active"}},
			{"labels":{"alertname":"OtherCluster","pod":"checkout","namespace":"payments","cluster":"other"},
			 "fingerprint":"2222222222222222","startsAt":"2026-11-11T08:00:00Z","endsAt":"2026-11-11T09:00:00Z","status":{"state":"active"}},
			{"labels":{"alertname":"OtherNamespace","pod":"checkout","namespace":"other","cluster":"prod"},
			 "fingerprint":"3333333333333333","startsAt":"2026-11-11T08:00:00Z","endsAt":"2026-11-11T09:00:00Z","status":{"state":"active"}},
			{"labels":{"alertname":"ContainerRestart","pod":"checkout","namespace":"payments","cluster":"prod","severity":"warning"},
			 "annotations":{"summary":"private annotation"},"fingerprint":"4444444444444444",
			 "startsAt":"2026-11-11T07:59:59Z","endsAt":"2026-11-11T08:15:00Z","status":{"state":"suppressed"}},
			{"labels":{"alertname":"PodNotReady","pod":"checkout","namespace":"payments","cluster":"prod","severity":"critical"},
			 "fingerprint":"5555555555555555","startsAt":"2026-11-11T08:10:00Z","endsAt":"2026-11-11T09:00:00Z","status":{"state":"active"}}
		]`)
	}))
	defer server.Close()
	source, err := New(observability.Config{URL: server.URL + "/am/", ClusterLabelValue: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := source.Alerts(context.Background(), query)
	if err != nil || len(signals) != 2 || requests.Load() != 1 {
		t.Fatalf("signals = %#v, err = %v, requests = %d", signals, err, requests.Load())
	}
	if signals[0].Name != "PodNotReady" || signals[0].LastSeen != query.Window.End ||
		signals[1].Name != "ContainerRestart" || signals[1].FirstSeen != query.Window.Start ||
		signals[1].Summary != "Alertmanager state suppressed" || signals[1].Severity != finding.SeverityWarning {
		t.Fatalf("unexpected normalized signals: %#v", signals)
	}

	query.Budget.MaxSignalsPerSource = 1
	signals, err = source.Alerts(context.Background(), query)
	if err != errIncomplete || len(signals) != 1 || signals[0].Name != "PodNotReady" {
		t.Fatalf("bounded evidence = %#v, error = %v", signals, err)
	}

	query.Scope.Resource.Name = `checkout",cluster=~".*`
	if _, err := source.Alerts(context.Background(), query); err == nil || requests.Load() != 2 {
		t.Fatal("invalid resource reached upstream")
	}
}

func TestAlertCandidateBudgetReportsPartial(t *testing.T) {
	start := time.Unix(1760000040, 0).UTC()
	end := start.Add(time.Minute)
	alerts := make([]upstreamAlert, maxCandidates+1)
	for i := range alerts {
		alerts[i] = upstreamAlert{
			Labels:   map[string]string{"alertname": "NodePressure", "node": "worker"},
			StartsAt: start.Format(time.RFC3339), EndsAt: end.Format(time.RFC3339),
			Fingerprint: fmt.Sprintf("%016x", i),
		}
		alerts[i].Status.State = "active"
	}
	body, err := json.Marshal(alerts)
	if err != nil {
		t.Fatal(err)
	}
	signals, err := decodeAlerts(body, finding.ResourceRef{Kind: "Node", Name: "worker"},
		[]matcher{{"node", "worker"}}, start, end, maxCandidates)
	if err != errIncomplete || len(signals) != maxCandidates {
		t.Fatalf("bounded candidates = %d, error = %v", len(signals), err)
	}
}
