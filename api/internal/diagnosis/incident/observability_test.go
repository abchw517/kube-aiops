package incident

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/observability"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestAlertmanagerScopedSnapshotAndDepartedHistory(t *testing.T) {
	d := ObservabilitySource{Shared: true, Label: "cluster", Value: "prod-a", IncludeSuppressed: true,
		Scope: ObservabilityScope{"production", "payments", "prod-a"}}
	entries := []map[string]any{
		{"labels": map[string]string{"alertname": "PaymentError", "cluster": "prod-a", "severity": "warning", "namespace": "payments"}, "startsAt": "2026-11-24T00:00:00Z", "status": map[string]string{"state": "active"}},
		{"labels": map[string]string{"alertname": "PaymentError", "cluster": "prod-a", "severity": "warning", "namespace": "payments"}, "startsAt": "2026-11-24T00:00:00Z", "status": map[string]string{"state": "active"}},
		{"labels": map[string]string{"alertname": "OtherCluster", "cluster": "prod-b", "severity": "critical"}, "startsAt": "2026-11-24T00:00:00Z", "status": map[string]string{"state": "active"}},
		{"labels": map[string]string{"alertname": "Muted", "cluster": "prod-a", "severity": "custom"}, "startsAt": "2026-11-24T00:00:00Z", "status": map[string]string{"state": "suppressed"}},
	}
	raw, _ := json.Marshal(entries)
	result, err := readObservabilityAlerts(d, raw, true)
	if err != nil || result.Count != 1 || result.Suppressed != 1 || len(result.Alerts) != 2 {
		t.Fatalf("%+v, %v", result, err)
	}
	if result.Alerts[0].Namespace != "payments" || result.Alerts[1].RawSeverity != "custom" || result.Alerts[1].Severity != "" {
		t.Fatalf("lost scope or original severity: %+v", result.Alerts)
	}
	snapshot, _ := json.Marshal(result.Alerts)
	now := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC).Unix()
	left := retainObservabilityAlerts(snapshot, nil, now)
	if len(left) != 2 || left[0].State != "departed" {
		t.Fatalf("disappearance was not preserved: %+v", left)
	}
	unchanged := retainObservabilityAlerts(snapshot, result.Alerts, now)
	if len(unchanged) != 2 || unchanged[0].State != "active" {
		t.Fatalf("repeated pull changed items: %+v", unchanged)
	}
}

func TestObservabilityPreviewValidatesPayloadAndDashboardSelection(t *testing.T) {
	if result, err := readObservabilitySeries("prometheus", []byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`)); err != nil || result.Count != 0 {
		t.Fatalf("valid empty vector: %+v %v", result, err)
	}
	if _, err := readObservabilitySeries("victorialogs", []byte(`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[2,"bad"]]}]}}`)); err == nil {
		t.Fatal("invalid aggregate was accepted")
	}
	raw := []byte(`[{"uid":"ops","title":"Ops","url":"https://invalid.example/"}]`)
	if _, err := readObservabilityDashboards(raw, []string{"missing"}, true); err == nil {
		t.Fatal("accepted inaccessible dashboard")
	}
	selected, err := readObservabilityDashboards(raw, []string{"ops"}, true)
	if err != nil || selected.Count != 1 || selected.Dashboards[0].URL != "" {
		t.Fatalf("accepted upstream link: %+v %v", selected, err)
	}
	for _, raw := range []string{"http://grafana", "https://a@host", "https://host/?token=secret", "https://host/a/../b", "https://host/%2f"} {
		if _, err := observability.ManagedEndpoint(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if got, err := observability.ManagedEndpoint("https://grafana.example.com/proxy/"); err != nil || got != "https://grafana.example.com/proxy" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestObservabilityReadEnforcesScopeAndKeepsCredentialsPrivate(t *testing.T) {
	fixture := newFixture(t)
	server := fixture.server
	server.Zabbix = &ZabbixIntegration{
		Authorize: func(_ context.Context, p identity.Principal, _ string, _ ZabbixScope) (bool, error) {
			return p.Subject == "admin", nil
		},
		ClusterBinding: func(_ context.Context, _ ZabbixScope, cluster string) (bool, error) { return cluster == "demo", nil },
	}
	server.Observability = &ObservabilityIntegration{
		Allowed:        func(_, _ string) bool { return true },
		AlertNamespace: func(_ context.Context, _ ObservabilityScope, ns string) (bool, error) { return ns == "allowed", nil },
	}
	server.registerObservability(fixture.mux)
	scope := ObservabilityScope{Environment: "testing", Business: "ops", Cluster: "demo"}
	spec, _ := json.Marshal(ObservabilitySource{Kind: "alertmanager", Name: "Test Alerts", Scope: scope, Endpoint: "https://monitor.example", Auth: "bearer"})
	snapshot, _ := json.Marshal([]ObservabilityAlert{
		{ID: "a", Title: "Allowed", Namespace: "allowed", Scope: scope, State: "active"},
		{ID: "b", Title: "OtherBusiness", Namespace: "private", Scope: scope, State: "active"},
	})
	_, err := fixture.db.ExecContext(t.Context(), `INSERT INTO diagnostic_observability
  (id,kind,environment,business,cluster,spec,credential,snapshot) VALUES (?,?,?,?,?,?,?,?)`,
		"am", "alertmanager", "testing", "ops", "demo", spec, []byte("test-private-ciphertext"), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	list := fixture.request(http.MethodGet, "/observability/sources", "admin", "", "", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), "Test Alerts") || strings.Contains(list.Body.String(), "test-private-ciphertext") {
		t.Fatalf("unexpected source disclosure: %s", list.Body.String())
	}
	restricted := fixture.request(http.MethodGet, "/observability/sources", "reader", "", "", "")
	if restricted.Code != 200 || strings.Contains(restricted.Body.String(), "Test Alerts") {
		t.Fatalf("restricted source leaked: %s", restricted.Body.String())
	}
	alerts := fixture.request(http.MethodGet, "/observability/alerts", "admin", "", "", "")
	if alerts.Code != 200 || !strings.Contains(alerts.Body.String(), "Allowed") || strings.Contains(alerts.Body.String(), "OtherBusiness") {
		t.Fatalf("namespace isolation failed: %s", alerts.Body.String())
	}
}
