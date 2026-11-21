package incident

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func requestJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDiagnosticV2RCAVersionsAndLateCancellation(t *testing.T) {
	f := newFixture(t)
	i, _ := createFixture(t, f, "prod", "rca-test")
	path := "/incidents/" + i.ID
	i = decoded[Incident](t, f.request("POST", path+"/collect", "admin", "", "", `"1"`), 200)
	ev := decoded[Page[Evidence]](t, f.request("GET", path+"/evidence", "admin", "", "", ""), 200)
	eid := ev.Items[0].ID
	modelResult := RCAConclusion{State: "supported", Judgment: "evidence available", References: []string{eid, "fake-reference"}, Gaps: "verify", Next: "inspect status"}
	f.server.Model = func(_ context.Context, _ Incident, _ string, data []byte) (string, error) {
		if strings.Contains(string(data), "supersecret-fixture") {
			return "", errors.New("unsafe model input")
		}
		return requestJSON(t, modelResult), nil
	}
	input := requestJSON(t, RCAInput{Observation: "operator notes", EvidenceIDs: []string{eid}})
	a := decoded[RCATask](t, f.request("POST", path+"/rca-runs", "admin", input, "request-one", ""), 202)
	retry := decoded[RCATask](t, f.request("POST", path+"/rca-runs", "admin", input, "request-one", ""), 202)
	if retry.ID != a.ID || a.State != "queued" {
		t.Fatalf("queue repeat: %+v", retry)
	}
	decoded[map[string]any](t, f.request("POST", path+"/rca-runs", "admin", input, "other-key", ""), 409)
	j, err := f.server.Store.claimRun(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.server.executeRCA(t.Context(), j)
	runPath := path + "/rca-runs/" + a.ID
	out := decoded[RCATask](t, f.request("GET", runPath, "admin", "", "", ""), 200)
	if out.State != "succeeded" || len(out.Revisions) != 1 || !slices.Equal(out.Revisions[0].Conclusion.References, []string{eid}) {
		t.Fatalf("model must drop forged reference: %+v", out)
	}
	modelResult.Judgment = "reviewed conclusion"
	revision := decoded[RCARevision](t, f.request("PATCH", runPath+"/conclusion", "admin", requestJSON(t, modelResult), "", `"3"`), 200)
	if revision.Version != 4 {
		t.Fatalf("amendment did not create new version: %+v", revision)
	}
	decoded[map[string]any](t, f.request("PATCH", runPath+"/conclusion", "admin", requestJSON(t, modelResult), "", `"3"`), 412)

	started := make(chan struct{})
	f.server.Model = func(ctx context.Context, _ Incident, _ string, _ []byte) (string, error) {
		close(started)
		<-ctx.Done()
		return requestJSON(t, modelResult), nil
	}
	b := decoded[RCATask](t, f.request("POST", path+"/rca-runs", "admin", input, "cancel-this", ""), 202)
	j, err = f.server.Store.claimRun(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { f.server.executeRCA(t.Context(), j); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel := f.request("POST", path+"/rca-runs/"+b.ID+"/cancel", "admin", "", "", "")
	if cancel.Code != 204 {
		t.Fatalf("cancel %d %s", cancel.Code, cancel.Body)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not observe cancellation")
	}
	end := decoded[RCATask](t, f.request("GET", path+"/rca-runs/"+b.ID, "admin", "", "", ""), 200)
	if end.State != "canceled" || len(end.Revisions) != 0 {
		t.Fatalf("late result escaped fence: %+v", end)
	}

	f.server.Model = func(context.Context, Incident, string, []byte) (string, error) {
		return "", context.DeadlineExceeded
	}
	timeoutTask := decoded[RCATask](t, f.request("POST", path+"/rca-runs", "admin", input, "timed-out-task", ""), 202)
	j, err = f.server.Store.claimRun(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.server.executeRCA(t.Context(), j)
	timed := decoded[RCATask](t, f.request("GET", path+"/rca-runs/"+timeoutTask.ID, "admin", "", "", ""), 200)
	previous := decoded[RCATask](t, f.request("GET", runPath, "admin", "", "", ""), 200)
	if timed.State != "timed_out" || timed.Observation != "operator notes" || len(previous.Revisions) != 2 {
		t.Fatalf("timeout lost observation or earlier result: %+v / %+v", timed, previous)
	}
	decoded[Incident](t, f.request("POST", path+"/split", "admin",
		requestJSON(t, SplitRequest{EvidenceIDs: []string{eid}, Title: "fresh incident", Reason: "move snapshot"}), "", `"2"`), 201)
	out = decoded[RCATask](t, f.request("GET", runPath, "admin", "", "", ""), 200)
	if out.ErrorCode != "evidence_changed" || len(out.Evidence) != 0 || len(out.Revisions) != 0 {
		t.Fatalf("moved evidence leaked an old conclusion: %+v", out)
	}
	old := rcaEvidence{Evidence: Evidence{ID: eid, ObservedAt: nowUTC().Add(-time.Hour)}, Historical: true}
	if result, ok := validateConclusion(modelResult, []rcaEvidence{old}, nowUTC()); !ok || result.State != "insufficient" {
		t.Fatalf("historical evidence asserted current conclusion: %+v", result)
	}
}

func TestDiagnosticV2TopologyCyclesAndVisibility(t *testing.T) {
	f := newFixture(t)
	f.server.Resolve = func(_ context.Context, _ identity.Principal, r CreateRequest) (Incident, error) {
		scope := r.Scope
		scope.EnvironmentID = "prod"
		return Incident{Scope: scope, ResourceID: scope.ClusterID, Resource: finding.ResourceRef{Kind: "Pod", Name: scope.ClusterID}},
			nil
	}
	f.server.Authorize = func(_ context.Context, p identity.Principal, _ string, scope Scope, _ bool) (bool, error) {
		return p.Subject == "admin" || scope.ClusterID != "secret", nil
	}
	a, _ := createFixture(t, f, "visible-one", "a")
	b, _ := createFixture(t, f, "visible-two", "b")
	c, _ := createFixture(t, f, "secret", "c")
	for _, edge := range [][2]string{{a.ID, b.ID}, {b.ID, a.ID}, {a.ID, c.ID}} {
		response := f.request("POST", "/incidents/"+edge[0]+"/topology/links", "admin",
			requestJSON(t, TopologyInput{TargetIncidentID: edge[1], Relation: "depends_on", Confidence: "confirmed", Reason: "fixture relationship"}), "", "")
		if response.Code != 204 {
			t.Fatalf("add edge: %d %s", response.Code, response.Body)
		}
	}
	graph := decoded[Topology](t, f.request("GET", "/incidents/"+a.ID+"/topology?depth=4", "reader", "", "", ""), 200)
	if len(graph.Nodes) != 2 || len(graph.Edges) != 2 || graph.Truncated ||
		slices.ContainsFunc(graph.Nodes, func(n TopologyNode) bool { return n.Name == "secret" }) {
		t.Fatalf("graph includes hidden nodes or cannot traverse cycle: %+v", graph)
	}
}

type connectorFixture struct{ err error }

const testCollectorKey = "fixture-only-long-connector-key-2026"

func (*connectorFixture) Validate(assetmeta.Config) error { return nil }
func (c *connectorFixture) Assets(_ context.Context, cfg assetmeta.Config, _, _ string) ([]assetmeta.Asset, error) {
	return []assetmeta.Asset{{ExternalID: cfg.AssetIDs[0], Name: "fixture asset", Kind: "Host", State: "monitored"}}, c.err
}
func (*connectorFixture) IngressToken(string, string, assetmeta.Config) (string, error) {
	return testCollectorKey, nil
}

func TestDiagnosticV2DatasourceAndIngressScope(t *testing.T) {
	f := newFixture(t)
	connector := &connectorFixture{}
	f.server.Assets = connector
	i, _ := createFixture(t, f, "prod", "mapped")
	other, _ := createFixture(t, f, "prod", "unmapped")
	ref := "collector-test-ref"
	in := DatasourceInput{Name: "zabbix fixture", Scope: i.Scope, Enabled: true, CredentialRef: "inventory-test-ref", IngressRef: &ref,
		Config: assetmeta.Config{Kind: "zabbix", AssetIDs: []string{"10010"}}}
	response := f.request("POST", "/datasources", "admin", requestJSON(t, in), "", "")
	value := decoded[Datasource](t, response, 201)
	if strings.Contains(response.Body.String(), "test-ref") || !value.IngressConfigured {
		t.Fatal("credential reference exposed")
	}
	ds := "/datasources/" + value.ID
	connector.err = assetmeta.ErrAuthorization
	unusable := decoded[Datasource](t, f.request("POST", ds+"/test", "admin", "", "", `"1"`), 200)
	if unusable.Health != "unauthorized" || unusable.SyncedAt != nil {
		t.Fatalf("failed sync saved asset metadata: %+v", unusable)
	}
	connector.err = nil
	healthy := decoded[Datasource](t, f.request("POST", ds+"/test", "admin", "", "", `"2"`), 200)
	if healthy.Health != "available" || healthy.Version != 3 || healthy.SyncedAt == nil {
		t.Fatalf("sync: %+v", healthy)
	}
	inventory := decoded[struct {
		Items []inventoryAsset `json:"items"`
	}](t, f.request("GET", ds+"/assets", "admin", "", "", ""), 200)
	if len(inventory.Items) != 1 {
		t.Fatalf("inventory: %+v", inventory)
	}
	link := f.request("POST", "/incidents/"+i.ID+"/topology/links", "admin",
		requestJSON(t, TopologyInput{TargetAssetID: inventory.Items[0].ID, Relation: "monitored_by", Confidence: "confirmed", Reason: "mapped"}), "", "")
	if link.Code != 204 {
		t.Fatalf("link to inventory: %d %s", link.Code, link.Body)
	}
	ingress := func(key, id string, at time.Time, state string) int {
		input := IngressInput{IncidentID: id, AssetID: "10010", ExternalID: "evt-1", State: state, ObservedAt: at, Summary: "token=supersecret-fixture"}
		r := httptest.NewRequest("POST", base+"/ingress/"+value.ID+"/signals", strings.NewReader(requestJSON(t, input)))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		f.mux.ServeHTTP(w, r)
		return w.Code
	}
	now := nowUTC()
	if a, b := ingress("invalid", i.ID, now, "firing"), ingress(testCollectorKey, other.ID, now, "firing"); a != 401 || b != 404 {
		t.Fatalf("credential or explicit link check failed: %d %d", a, b)
	}
	for _, status := range []int{
		ingress(testCollectorKey, i.ID, now, "resolved"),
		ingress(testCollectorKey, i.ID, now.Add(-time.Second), "firing"),
		ingress(testCollectorKey, i.ID, now, "resolved"),
	} {
		if status != 202 {
			t.Fatalf("replay returned %d", status)
		}
	}
	ev := decoded[Page[Evidence]](t, f.request("GET", "/incidents/"+i.ID+"/evidence", "admin", "", "", ""), 200)
	if len(ev.Items) != 1 || ev.Items[0].State != "resolved" || strings.Contains(ev.Items[0].Summary, "supersecret-fixture") {
		t.Fatalf("invalid projected evidence: %+v", ev)
	}
	host := decoded[Incident](t, f.request("POST", "/incidents", "admin", requestJSON(t,
		CreateRequest{AssetID: inventory.Items[0].ID, Title: "host incident", Scope: i.Scope, Severity: "P2"}), "host-incident", ""), 201)
	hostPath := "/incidents/" + host.ID
	assetIncident := decoded[Page[Incident]](t, f.request("GET", "/incidents?platform=zabbix", "admin", "", "", ""), 200)
	if len(assetIncident.Items) != 1 || assetIncident.Items[0].ID != host.ID || host.Resource.Kind != "Host" {
		t.Fatalf("asset incident was not attributed to its platform: %+v", assetIncident)
	}
	if code := ingress(testCollectorKey, host.ID, now, "resolved"); code != 202 {
		t.Fatalf("asset replay: %d", code)
	}
	moved := decoded[Page[Evidence]](t, f.request("GET", hostPath+"/evidence", "admin", "", "", ""), 200)
	if len(moved.Items) != 0 {
		t.Fatalf("asset signal duplicated across incident anchors: %+v", moved)
	}
	in.Enabled = false
	decoded[Datasource](t, f.request("PATCH", ds, "admin", requestJSON(t, in), "", `"3"`), 200)
	if code := ingress(testCollectorKey, i.ID, now, "resolved"); code != 401 {
		t.Fatalf("disabled datasource accepted input: %d", code)
	}
}

func TestDiagnosticV2PolicyIsScopedVersionedAndDeduplicated(t *testing.T) {
	f := newFixture(t)
	f.server.AutomationEnabled = func(s Scope) bool { return s.EnvironmentID == "prod" }
	i, _ := createFixture(t, f, "prod", "policy-fixture")
	input := PolicyInput{Name: "fixture", Scope: i.Scope, Enabled: true, Source: "prometheus", Template: "evidence_rca", CooldownSeconds: 60, MaxConcurrent: 1}
	p := decoded[Policy](t, f.request("POST", "/policies", "admin", requestJSON(t, input), "", ""), 201)
	path := "/incidents/" + i.ID
	decoded[Incident](t, f.request("POST", path+"/collect", "admin", "", "", `"1"`), 200)
	for range 2 {
		if err := f.server.evaluatePolicies(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	jobs := decoded[Page[RCATask]](t, f.request("GET", path+"/rca-runs", "admin", "", "", ""), 200)
	if len(jobs.Items) != 1 || jobs.Items[0].PolicyVersion != p.Version || jobs.Items[0].State != "queued" {
		t.Fatalf("automation did not deduplicate: %+v", jobs)
	}
	input.Enabled = false
	decoded[Policy](t, f.request("PATCH", "/policies/"+p.ID, "admin", requestJSON(t, input), "", `"1"`), 200)
	decoded[map[string]any](t, f.request("PATCH", "/policies/"+p.ID, "admin", requestJSON(t, input), "", `"1"`), 412)
	history := decoded[struct {
		Items []PolicyExecution `json:"items"`
	}](t, f.request("GET", "/policies/"+p.ID+"/runs", "admin", "", "", ""), 200)
	if len(history.Items) != 1 || history.Items[0].State != "canceled" {
		t.Fatalf("disabled policy did not retain execution history: %+v", history)
	}
}
