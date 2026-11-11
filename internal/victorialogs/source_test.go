package victorialogs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/observability"
)

func TestScopedLogAggregationAndBudget(t *testing.T) {
	start := time.Unix(1760000035, 0).UTC()
	end := start.Add(time.Minute)
	query := correlation.Query{
		Scope: correlation.CorrelationScope{Cluster: finding.LocalCluster, Namespace: "payments",
			Resource: finding.ResourceRef{Kind: "Pod", Namespace: "payments", Name: "checkout"}},
		Window: correlation.TimeWindow{Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339)},
		Budget: correlation.CorrelationBudget{WindowSeconds: 60, PerSourceTimeoutMillis: 1000, MaxSignalsPerSource: 2},
	}
	firstBucket := start.Truncate(time.Minute)
	secondBucket := firstBucket.Add(time.Minute)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		params := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/logs/select/logsql/stats_query_range" ||
			params.Get("start") != query.Window.Start || params.Get("end") != query.Window.End ||
			params.Get("step") != "1m" || params.Get("timeout") != "900ms" {
			t.Errorf("unexpected VictoriaLogs request: %s %s", r.Method, r.URL)
		}
		logsQL := params.Get("query")
		if !strings.HasPrefix(logsQL, `{"kubernetes.pod_namespace"="payments","kubernetes.pod_name"="checkout"} cluster:="prod" | stats `) ||
			strings.Count(logsQL, "count() if") != 6 {
			t.Errorf("unexpected query catalog or scope: %s", logsQL)
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"__name__":"out_of_memory","hidden":"private"},"values":[[%d,"1"]]},
			{"metric":{"__name__":"panic_fatal"},"values":[[%d,"2"],[%d,"3"]]},
			{"metric":{"__name__":"application_restart"},"values":[[%d,"0"]]}
		]}}`, secondBucket.Unix(), firstBucket.Unix(), secondBucket.Unix(), secondBucket.Unix())
	}))
	defer server.Close()
	source, err := New(observability.Config{URL: server.URL + "/logs", ClusterLabelValue: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := source.LogSignals(context.Background(), query)
	if err != nil || len(signals) != 2 || requests.Load() != 1 {
		t.Fatalf("signals = %#v, err = %v, requests = %d", signals, err, requests.Load())
	}
	if signals[0].Name != "panic_fatal" || signals[0].Count != 5 || signals[0].FirstSeen != query.Window.Start ||
		signals[0].LastSeen != secondBucket.Format(time.RFC3339) || !strings.HasPrefix(signals[0].Fingerprint, "sha256:") ||
		signals[1].Name != "out_of_memory" || signals[1].Count != 1 {
		t.Fatalf("unexpected normalized signals: %#v", signals)
	}

	query.Budget.MaxSignalsPerSource = 1
	signals, err = source.LogSignals(context.Background(), query)
	if err != errIncomplete || len(signals) != 1 || requests.Load() != 2 {
		t.Fatalf("bounded evidence = %#v, err = %v, requests = %d", signals, err, requests.Load())
	}

	query.Scope.Resource.Namespace = "other"
	if _, err := source.LogSignals(context.Background(), query); err == nil || requests.Load() != 2 {
		t.Fatal("invalid namespace reached upstream")
	}
}

func TestMalformedLogSeriesRetainsValidEvidence(t *testing.T) {
	start := time.Unix(1760000040, 0).UTC()
	end := start.Add(time.Minute)
	body := fmt.Appendf(nil, `{"status":"success","data":{"resultType":"matrix","result":[
		{"metric":{"__name__":"panic_fatal"},"values":[[%d,"1"]]},
		{"metric":{"__name__":"out_of_memory"},"values":[[%d,"-1"]]}
	]}}`, start.Unix(), start.Unix())
	signals, err := decodeSignals(body, finding.ResourceRef{}, start, end, 6)
	if err != errIncomplete || len(signals) != 1 || signals[0].Name != "panic_fatal" {
		t.Fatalf("partial evidence = %#v, error = %v", signals, err)
	}
	empty := []byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	if signals, err := decodeSignals(empty, finding.ResourceRef{}, start, end, 6); err != nil || len(signals) != 0 {
		t.Fatalf("empty evidence = %#v, error = %v", signals, err)
	}
}
