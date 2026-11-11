package prometheus

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

var testEnd = time.Unix(1760000000, 0).UTC()

func testQuery(kind string) correlation.Query {
	namespace := "payments"
	if kind == "Node" {
		namespace = ""
	}
	return correlation.Query{
		Scope: correlation.CorrelationScope{
			Cluster: finding.LocalCluster, Namespace: namespace,
			Resource: finding.ResourceRef{Kind: kind, Namespace: namespace, Name: "checkout"},
		},
		Window: correlation.TimeWindow{
			Start: testEnd.Add(-time.Minute).Format(time.RFC3339),
			End:   testEnd.Format(time.RFC3339),
		},
		Budget: correlation.CorrelationBudget{
			WindowSeconds: 60, PerSourceTimeoutMillis: 1000, MaxSignalsPerSource: 50,
		},
	}
}

func TestPodRangeQueriesReturnScopedPeakEvidence(t *testing.T) {
	query := testQuery("Pod")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		params := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/prom/api/v1/query_range" ||
			params.Get("start") != query.Window.Start || params.Get("end") != query.Window.End ||
			params.Get("step") != "60" || params.Get("limit") != "1" || params.Get("timeout") != "900ms" {
			t.Errorf("unexpected range request: %s %s", r.Method, r.URL)
		}
		if !strings.Contains(params.Get("query"), `namespace="payments",pod="checkout",cluster="production"`) {
			t.Errorf("unscoped query: %s", params.Get("query"))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"ignored":"raw-upstream-label"},"values":[[%d,"9"],[%d,"2"]]}]}}`,
			testEnd.Add(-time.Minute).Unix(), testEnd.Unix())
	}))
	defer server.Close()

	source, err := New(Config{URL: server.URL + "/prom/", ClusterLabelValue: "production"})
	if err != nil {
		t.Fatal(err)
	}
	signals, err := source.Metrics(context.Background(), query)
	if err != nil || len(signals) != 5 || calls.Load() != 5 {
		t.Fatalf("metrics = %d, requests = %d, error = %v", len(signals), calls.Load(), err)
	}
	for i, signal := range signals {
		metric := metricCatalog["Pod"][i]
		if signal.Name != metric.name || signal.Unit != metric.unit || signal.Count != 2 ||
			signal.Value == nil || *signal.Value != 9 || signal.FirstSeen != query.Window.Start ||
			signal.LastSeen != query.Window.End || *signal.Resource != query.Scope.Resource {
			t.Errorf("unexpected normalized evidence: %#v", signal)
		}
	}
	query.Budget.MaxSignalsPerSource = 2
	signals, err = source.Metrics(context.Background(), query)
	if err == nil || len(signals) != 2 {
		t.Fatalf("signal budget = %d, error = %v", len(signals), err)
	}
}

func TestInstantQueryUsesTLSAndScopedSelectors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := r.URL.Query()
		if r.URL.Path != "/api/v1/query" || params.Get("time") != testQuery("Node").Window.End ||
			r.Header.Get("Authorization") != "Bearer test-only" {
			t.Errorf("unexpected instant request: %s", r.URL)
		}
		expression := params.Get("query")
		if !strings.Contains(expression, `namespace="payments",deployment="checkout"`) &&
			!strings.Contains(expression, `node="checkout",status="true"`) {
			t.Errorf("unscoped instant query: %s", expression)
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[%d,"0"]}]}}`, testEnd.Unix())
	}))
	defer server.Close()

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("test-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := New(Config{URL: server.URL, CAFile: caFile, TokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"Deployment", "Node"} {
		signals, err := source.Metrics(context.Background(), testQuery(kind))
		if err != nil || len(signals) != 1 || signals[0].Value == nil || *signals[0].Value != 0 || signals[0].Count != 1 {
			t.Errorf("%s: unexpected instant result %#v, error %v", kind, signals, err)
		}
	}
}

func TestCorrelationKeepsEvidenceWhenPrometheusQueriesFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "container_memory_working_set_bytes") {
			fmt.Fprintf(w, `{"status":"success","warnings":["private upstream warning"],"data":{
				"resultType":"matrix","result":[{"values":[[%d,"42"]]}]}}`, testEnd.Unix())
			return
		}
		http.Error(w, "private upstream failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	source, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	service, err := correlation.NewService(correlation.ServiceOptions{
		Prometheus: source, Window: time.Minute, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := service.Correlate(context.Background(), correlation.Request{
		FindingID: "finding-1", Scope: testQuery("Pod").Scope, AnchorTime: testEnd.Format(time.RFC3339),
	})
	if err != nil || len(bundle.Signals) != 1 || bundle.Signals[0].Name != "pod_memory_working_set" {
		t.Fatalf("unexpected bundle %#v, error %v", bundle, err)
	}
	if bundle.Sources[1].Source != correlation.SourcePrometheus || bundle.Sources[1].State != correlation.SourcePartial {
		t.Fatalf("unexpected source states: %#v", bundle.Sources)
	}
}

func TestRejectsUnsafeScopeAndDistinguishesEmptyFromFailed(t *testing.T) {
	var calls atomic.Int32
	var response atomic.Value
	response.Store(`{"status":"success","data":{"resultType":"vector","result":[]}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		io.WriteString(w, response.Load().(string))
	}))
	defer server.Close()
	source, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	query := testQuery("Deployment")
	query.Scope.Resource.Name = `checkout",namespace=~".*`
	if _, err := source.Metrics(context.Background(), query); err == nil || calls.Load() != 0 {
		t.Fatal("unsafe resource name reached the upstream")
	}
	query = testQuery("Deployment")
	query.Scope.Resource.Namespace = "other"
	if _, err := source.Metrics(context.Background(), query); err == nil || calls.Load() != 0 {
		t.Fatal("mismatched namespace reached the upstream")
	}

	query = testQuery("Deployment")
	if signals, err := source.Metrics(context.Background(), query); err != nil || len(signals) != 0 {
		t.Fatalf("empty result = %v, %v", signals, err)
	}
	for _, body := range []string{
		`{"status":"error","error":"private upstream detail"}`,
		`{"status":"success","data":{"resultType":"vector","result":[{},{}]}}`,
		fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"value":[%d,"7"]}]}}`, testEnd.Add(time.Second).Unix()),
		strings.Repeat(" ", maxResponseBytes+1),
	} {
		response.Store(body)
		if signals, err := source.Metrics(context.Background(), query); err != errQuery || len(signals) != 0 {
			t.Fatalf("invalid response = %v, %v", signals, err)
		}
	}
}

func TestOptionalSourceAndRedirectBoundary(t *testing.T) {
	source, err := New(Config{})
	if err != nil || source != nil {
		t.Fatalf("empty configuration = %v, %v", source, err)
	}
	var requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer origin.Close()
	source, err = New(Config{URL: origin.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Metrics(context.Background(), testQuery("Deployment")); err != errQuery || requests.Load() != 0 {
		t.Fatalf("redirect boundary: error %v, destination requests %d", err, requests.Load())
	}
}

func TestSourceTimeoutRetainsEvidenceFromCompletedQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "container_memory_working_set_bytes") {
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"42"]]}]}}`, testEnd.Unix())
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	source, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	query := testQuery("Pod")
	query.Budget.PerSourceTimeoutMillis = 300
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	signals, err := source.Metrics(ctx, query)
	if ctx.Err() != nil || err != errQuery || len(signals) != 1 || signals[0].Name != "pod_memory_working_set" {
		t.Fatalf("source timeout returned %v, %v, context error %v", signals, err, ctx.Err())
	}
}
