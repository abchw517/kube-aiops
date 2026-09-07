package kubernetes

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

func TestEventsUsesFixedBoundedRequestAndNormalizesSignals(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method=%s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/namespaces/prod/events" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer event-test-token" {
			t.Errorf("authorization header missing")
		}
		selector, err := url.QueryUnescape(r.URL.Query().Get("fieldSelector"))
		if err != nil {
			t.Errorf("decode selector: %v", err)
		}
		if selector != "involvedObject.kind=Pod,involvedObject.name=demo" {
			t.Errorf("fieldSelector=%q", selector)
		}
		if r.URL.Query().Get("limit") != "8" {
			t.Errorf("limit=%q, want 8", r.URL.Query().Get("limit"))
		}

		payload := map[string]any{"items": []map[string]any{
			{
				"metadata":       map[string]any{"name": "demo-new", "namespace": "prod", "uid": "uid-new", "creationTimestamp": "2026-09-07T01:58:00Z"},
				"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "namespace": "prod", "name": "demo"},
				"reason":         "BackOff", "message": "Back-off restarting failed container", "type": "Warning", "count": 3,
				"firstTimestamp": "2026-09-07T01:50:00Z", "lastTimestamp": "2026-09-07T01:59:00Z",
			},
			{
				"metadata":       map[string]any{"name": "demo-old", "namespace": "prod", "uid": "uid-old", "creationTimestamp": "2026-09-07T01:40:00Z"},
				"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "namespace": "prod", "name": "demo"},
				"reason":         "Scheduled", "message": "Successfully assigned", "type": "Normal", "count": 1,
				"firstTimestamp": "2026-09-07T01:40:00Z", "lastTimestamp": "2026-09-07T01:41:00Z",
			},
			{
				"metadata":       map[string]any{"name": "wrong-pod", "namespace": "prod", "uid": "uid-wrong", "creationTimestamp": "2026-09-07T01:57:00Z"},
				"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "namespace": "prod", "name": "other"},
				"reason":         "BackOff", "message": "must be ignored", "type": "Warning", "count": 9,
			},
			{
				"metadata":       map[string]any{"name": "too-old", "namespace": "prod", "uid": "uid-too-old", "creationTimestamp": "2026-09-06T22:00:00Z"},
				"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "namespace": "prod", "name": "demo"},
				"reason":         "Old", "message": "outside window", "type": "Normal", "count": 1,
			},
		}}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newEventTestClient(t, server, "event-test-token")
	signals, err := client.Events(context.Background(), correlation.Query{
		Scope: correlation.CorrelationScope{
			Cluster:   "local",
			Namespace: "prod",
			Resource:  resourceRef("v1", "Pod", "prod", "demo"),
		},
		Window: correlation.TimeWindow{Start: "2026-09-07T01:30:00Z", End: "2026-09-07T02:00:00Z"},
		Budget: correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 2},
	})
	if err != nil {
		t.Fatalf("Events() error=%v", err)
	}
	if len(signals) != 2 {
		t.Fatalf("signals=%d, want 2", len(signals))
	}
	if signals[0].ID != "uid-new" || signals[0].Name != "BackOff" || signals[0].Severity != "warning" || signals[0].Count != 3 {
		t.Fatalf("unexpected first signal: %#v", signals[0])
	}
	if signals[0].Source != correlation.SourceKubernetesEvents || signals[0].Type != correlation.SignalTypeEvent {
		t.Fatalf("unexpected source/type: %#v", signals[0])
	}
	if signals[0].Summary != "BackOff: Back-off restarting failed container" {
		t.Fatalf("summary=%q", signals[0].Summary)
	}
	if signals[0].Resource == nil || signals[0].Resource.Name != "demo" || signals[0].Resource.Namespace != "prod" {
		t.Fatalf("resource=%#v", signals[0].Resource)
	}
	if len(signals[0].Evidence) != 1 || signals[0].Evidence[0].ID != "uid-new" {
		t.Fatalf("evidence=%#v", signals[0].Evidence)
	}
	if signals[1].ID != "uid-old" || signals[1].Severity != "info" {
		t.Fatalf("unexpected second signal: %#v", signals[1])
	}
}

func TestEventListRequestRejectsSelectorInjectionAndScopeMismatch(t *testing.T) {
	base := correlation.Query{
		Scope: correlation.CorrelationScope{
			Cluster:   "local",
			Namespace: "prod",
			Resource:  resourceRef("v1", "Pod", "prod", "demo"),
		},
		Window: correlation.TimeWindow{Start: "2026-09-07T01:30:00Z", End: "2026-09-07T02:00:00Z"},
		Budget: correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 50},
	}

	injected := base
	injected.Scope.Resource.Name = "demo,reason=Failed"
	if _, _, _, _, err := eventListRequest(injected); err == nil || !strings.Contains(err.Error(), "selector") {
		t.Fatalf("selector injection error=%v", err)
	}

	mismatch := base
	mismatch.Scope.Resource.Namespace = "restricted"
	if _, _, _, _, err := eventListRequest(mismatch); err == nil || !strings.Contains(err.Error(), "namespace mismatch") {
		t.Fatalf("scope mismatch error=%v", err)
	}

	foreign := base
	foreign.Scope.Cluster = "foreign"
	if _, _, _, _, err := eventListRequest(foreign); err == nil {
		t.Fatal("foreign cluster unexpectedly accepted")
	}
}

func TestEventListRequestClusterScopeIsFixedAndBounded(t *testing.T) {
	endpoint, _, _, limit, err := eventListRequest(correlation.Query{
		Scope: correlation.CorrelationScope{
			Cluster:  "local",
			Resource: resourceRef("apps/v1", "Deployment", "", "api"),
		},
		Window: correlation.TimeWindow{Start: "2026-09-07T01:30:00Z", End: "2026-09-07T02:00:00Z"},
		Budget: correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(endpoint, "/api/v1/events?") {
		t.Fatalf("endpoint=%q", endpoint)
	}
	values, err := url.ParseQuery(strings.SplitN(endpoint, "?", 2)[1])
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("limit") != "200" || limit != 100 {
		t.Fatalf("candidate=%q output=%d", values.Get("limit"), limit)
	}
	if values.Get("fieldSelector") != "involvedObject.kind=Deployment,involvedObject.name=api" {
		t.Fatalf("selector=%q", values.Get("fieldSelector"))
	}
}

func newEventTestClient(t *testing.T, server *httptest.Server, token string) *Client {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(dir, "ca.crt")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(caFile, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	return NewClient(Config{APIURL: server.URL, TokenFile: tokenFile, CAFile: caFile})
}

func resourceRef(apiVersion, kind, namespace, name string) finding.ResourceRef {
	return finding.ResourceRef{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name}
}
