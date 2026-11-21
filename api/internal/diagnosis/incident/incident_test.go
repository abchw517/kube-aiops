package incident

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const testOrigin = "https://portal.example.test"

type fixture struct {
	mux    *http.ServeMux
	db     *identitystore.Store
	server *Server
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := identitystore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := Open(t.Context(), db.DB, permissions.RecordDiagnosticOperation)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		Store: store, Origin: testOrigin,
		Authenticate: func(_ context.Context, r *http.Request) (identity.Principal, error) {
			if subject := r.Header.Get("X-Fixture-Account"); subject != "" {
				return identity.Principal{Subject: subject, Provider: "local"}, nil
			}
			return identity.Principal{}, errors.New("no test session")
		},
		Authorize: func(_ context.Context, p identity.Principal, code string, scope Scope, _ bool) (bool, error) {
			if p.Subject == "admin" {
				return true, nil
			}
			return scope.ClusterID != "pre" && (code == ReadPermission || code == ListPermission), nil
		},
		Resolve: func(_ context.Context, _ identity.Principal, req CreateRequest) (Incident, error) {
			scope := req.Scope
			scope.EnvironmentID = scope.ClusterID
			return Incident{Scope: scope, Resource: finding.ResourceRef{Kind: "Pod", Namespace: scope.Namespace, Name: "checkout"},
				ResourceID: scope.EnvironmentID + "/" + scope.Namespace + "/checkout", FindingID: req.FindingID}, nil
		},
		ActiveGroup: func(_ context.Context, id string) (bool, error) { return id == "ops", nil },
		Collect: func(_ context.Context, i Incident) (correlation.CorrelationBundle, error) {
			end := i.CreatedAt.Truncate(time.Second)
			start := end.Add(-30 * time.Minute)
			ref := i.Resource
			return correlation.CorrelationBundle{
				FindingID: i.ID, Scope: correlation.CorrelationScope{Cluster: i.Scope.ClusterID, Namespace: i.Scope.Namespace, Resource: ref},
				Window: correlation.TimeWindow{Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339)},
				Budget: correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 50},
				Sources: []correlation.SourceStatus{
					{Source: correlation.SourceKubernetesEvents, State: correlation.SourceAvailable},
					{Source: correlation.SourcePrometheus, State: correlation.SourceAvailable},
					{Source: correlation.SourceAlertmanager, State: correlation.SourceUnavailable},
					{Source: correlation.SourceVictoriaLogs, State: correlation.SourceDisabled},
				},
				Signals: []correlation.CorrelationSignal{
					{ID: "event-1", Source: correlation.SourceKubernetesEvents, Type: correlation.SignalTypeEvent, Resource: &ref,
						FirstSeen: start.Format(time.RFC3339), LastSeen: end.Format(time.RFC3339), Summary: "token=supersecret-fixture"},
					{ID: "metric-1", Source: correlation.SourcePrometheus, Type: correlation.SignalTypeMetric, Resource: &ref,
						FirstSeen: start.Format(time.RFC3339), LastSeen: end.Format(time.RFC3339), Name: "test_metric", Value: new(float64), Unit: correlation.UnitCount},
				},
			}, nil
		},
	}
	mux := http.NewServeMux()
	server.Register(mux)
	return fixture{mux, db, server}
}

func (f fixture) request(method, path, account, body, idempotency, version string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, base+path, strings.NewReader(body))
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("X-Fixture-Account", account)
	if idempotency != "" {
		r.Header.Set("Idempotency-Key", idempotency)
	}
	if version != "" {
		r.Header.Set("If-Match", version)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

func decoded[T any](t *testing.T, w *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
	}
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func createFixture(t *testing.T, f fixture, cluster, key string) (Incident, string) {
	t.Helper()
	body := `{"title":"Checkout outage","description":"token=supersecret-fixture","scope":{"clusterId":"` + cluster + `","namespace":"orders"},"severity":"P2"}`
	item := decoded[Incident](t, f.request("POST", "/incidents", "admin", body, key, ""), 201)
	if strings.Contains(item.Description, "supersecret-fixture") {
		t.Fatal("raw secret stored")
	}
	return item, body
}

func TestIncidentLifecycleIdempotencyAndAuthorization(t *testing.T) {
	f := newFixture(t)
	i, body := createFixture(t, f, "prod", "key-1")
	hidden, _ := createFixture(t, f, "pre", "key-2")
	if i.Version != 1 || i.Status != Pending || i.DisplayKey == "" {
		t.Fatalf("new incident: %+v", i)
	}
	path := "/incidents/" + i.ID
	f.server.Resolve = func(context.Context, identity.Principal, CreateRequest) (Incident, error) {
		return Incident{}, errors.New("source temporarily offline")
	}
	replay := decoded[Incident](t, f.request("POST", "/incidents", "admin", body, "key-1", ""), 200)
	if replay.ID != i.ID {
		t.Fatalf("idempotent replay changed ID: %+v", replay)
	}
	decoded[map[string]any](t, f.request("POST", "/incidents", "admin", strings.Replace(body, "Checkout outage", "Other outage", 1), "key-1", ""), 409)
	decoded[map[string]any](t, f.request("GET", path, "", "", "", ""), 401)
	decoded[map[string]any](t, f.request("GET", "/incidents/"+hidden.ID, "reader", "", "", ""), 404)
	decoded[map[string]any](t, f.request("GET", "/incidents?clusterId=pre", "reader", "", "", ""), 403)
	page := decoded[Page[Incident]](t, f.request("GET", "/incidents?limit=1", "reader", "", "", ""), 200)
	if len(page.Items) != 1 || page.Items[0].ID != i.ID || page.Pagination.Continue != "" {
		t.Fatalf("scope filter: %+v", page)
	}
	summary := decoded[Overview](t, f.request("GET", "/overview", "reader", "", "", ""), 200)
	if summary.Counts["total"] != 1 || summary.Counts[Pending] != 1 {
		t.Fatalf("summary leaks scope: %+v", summary)
	}
	decoded[map[string]any](t, f.request("PATCH", path, "reader", `{"status":"investigating","reason":"checked"}`, "", `"1"`), 403)
	decoded[map[string]any](t, f.request("PATCH", path, "admin", `{"status":"recovered","reason":"invalid skip"}`, "", `"1"`), 409)
	decoded[map[string]any](t, f.request("PATCH", path, "admin", `{"status":"investigating","reason":"checked"}`, "", ""), 428)

	statusCodes := make(chan int, 2)
	for range 2 {
		go func() {
			statusCodes <- f.request("PATCH", path, "admin", `{"status":"investigating","reason":"checked"}`, "", `"1"`).Code
		}()
	}
	a, b := <-statusCodes, <-statusCodes
	if a+b != 200+412 || a == b {
		t.Fatalf("optimistic concurrency: got %d and %d", a, b)
	}
	for index, status := range []string{Recovered, Closed} {
		v := strconv.Quote(strconv.Itoa(index + 2))
		updated := decoded[Incident](t, f.request("PATCH", path, "admin", `{"status":"`+status+`","reason":"checked"}`, "", v), 200)
		if updated.Status != status {
			t.Fatalf("status after update: %+v", updated)
		}
	}
	decoded[map[string]any](t, f.request("PATCH", path, "admin", `{"status":"investigating","reason":"terminal"}`, "", `"4"`), 409)
	timeline := decoded[Page[TimelineEntry]](t, f.request("GET", path+"/timeline?limit=2", "reader", "", "", ""), 200)
	if len(timeline.Items) != 2 || timeline.Pagination.Continue == "" || timeline.Items[0].After != Closed {
		t.Fatalf("timeline: %+v", timeline)
	}
	older := decoded[Page[TimelineEntry]](t, f.request("GET", path+"/timeline?limit=2&continue="+timeline.Pagination.Continue, "reader", "", "", ""), 200)
	if len(older.Items) != 2 || older.Items[1].Actor != "admin" || older.Items[1].Kind != "created" {
		t.Fatalf("older timeline: %+v", older)
	}
	var operations int
	if err := f.db.QueryRow("SELECT count(*) FROM operation_audit WHERE actor='admin' AND target=?", i.DisplayKey).Scan(&operations); err != nil || operations != 4 {
		t.Fatalf("expected four atomic audit records, got %d (%v)", operations, err)
	}
	pathname := f.db.Path()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := identitystore.Open(pathname)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reopened, err := Open(t.Context(), db.DB, permissions.RecordDiagnosticOperation)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := reopened.Get(t.Context(), i.ID)
	if err != nil || reloaded.Status != Closed {
		t.Fatalf("reloaded: %+v, %v", reloaded, err)
	}
}

func TestIncidentSourceReplayScopeAndOwnership(t *testing.T) {
	f := newFixture(t)
	i, _ := createFixture(t, f, "prod", "p")
	pre, _ := createFixture(t, f, "pre", "s")
	path := "/incidents/" + i.ID
	first := decoded[Incident](t, f.request("POST", path+"/collect", "admin", "", "", `"1"`), 200)
	replay := decoded[Incident](t, f.request("POST", path+"/collect", "admin", "", "", `"2"`), 200)
	if first.Version != 2 || replay.Version != first.Version {
		t.Fatalf("collection replay incremented version: %+v", replay)
	}
	detail := decoded[Detail](t, f.request("GET", path, "admin", "", "", ""), 200)
	if detail.Coverage != "partial" || detail.Counts["signals"] != 2 || len(detail.Incident.Sources) != 4 {
		t.Fatalf("cross-source result: %+v", detail)
	}
	page := decoded[Page[Evidence]](t, f.request("GET", path+"/evidence", "admin", "", "", ""), 200)
	if len(page.Items) != 2 || page.Items[0].Source == page.Items[1].Source {
		t.Fatalf("cross-source evidence: %+v", page)
	}
	for _, item := range page.Items {
		d := decoded[EvidenceDetail](t, f.request("GET", path+"/evidence/"+item.ID, "admin", "", "", ""), 200)
		if strings.Contains(d.Signal.Summary, "supersecret-fixture") {
			t.Fatalf("unsanitized evidence: %+v", d)
		}
	}
	decoded[map[string]any](t, f.request("GET", path+"/evidence/"+page.Items[0].ID, "reader", "", "", ""), 404)
	readOnly := decoded[Detail](t, f.request("GET", path, "reader", "", "", ""), 200)
	if readOnly.Actions.Evidence || readOnly.Counts != nil || len(readOnly.Incident.Sources) != 0 {
		t.Fatalf("permission projection: %+v", readOnly)
	}

	decoded[Incident](t, f.request("POST", "/incidents/"+pre.ID+"/collect", "admin", "", "", `"1"`), 200)
	decoded[map[string]any](t, f.request("POST", path+"/merge", "admin", `{"sourceId":"`+pre.ID+`","sourceVersion":2,"reason":"different scope"}`, "", `"2"`), 409)
	key := page.Items[0].ID
	child := decoded[Incident](t, f.request("POST", path+"/split", "admin", `{"evidenceIds":["`+key+`"],"title":"split observation","reason":"different symptom"}`, "", `"2"`), 201)
	parentPage := decoded[Page[Evidence]](t, f.request("GET", path+"/evidence", "admin", "", "", ""), 200)
	childPage := decoded[Page[Evidence]](t, f.request("GET", "/incidents/"+child.ID+"/evidence", "admin", "", "", ""), 200)
	if len(parentPage.Items) != 1 || len(childPage.Items) != 1 || childPage.Items[0].ID != key {
		t.Fatalf("split lost ownership: %+v / %+v", parentPage, childPage)
	}
	decoded[Incident](t, f.request("POST", path+"/merge", "admin", `{"sourceId":"`+child.ID+`","sourceVersion":1,"reason":"same incident"}`, "", `"3"`), 200)
	detail = decoded[Detail](t, f.request("GET", path, "admin", "", "", ""), 200)
	if detail.Counts["signals"] != 2 {
		t.Fatalf("merge lost signals: %+v", detail)
	}
	merged := decoded[Detail](t, f.request("GET", "/incidents/"+child.ID, "admin", "", "", ""), 200)
	if merged.Incident.Status != Closed || merged.Incident.MergedInto != i.ID {
		t.Fatalf("merge did not retain source: %+v", merged)
	}
}

func TestOutOfOrderSignalCannotRegressState(t *testing.T) {
	f := newFixture(t)
	i, _ := createFixture(t, f, "prod", "p")
	now := nowUTC()
	signal := Signal{Key: "firing-series", State: "resolved", First: now.Add(-time.Minute), Last: now,
		Value: correlation.CorrelationSignal{ID: "test", Source: correlation.SourceAlertmanager, Type: correlation.SignalTypeAlert}}
	first, err := f.server.Store.Correlate(t.Context(), i.ID, "admin", i.Version, []Signal{signal}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	signal.State, signal.Last = "firing", now.Add(-time.Second)
	got, err := f.server.Store.Correlate(t.Context(), i.ID, "admin", first.Version, []Signal{signal}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	var state string
	if err := f.db.QueryRow("SELECT state FROM diagnostic_signal WHERE incident_id=?", i.ID).Scan(&state); err != nil || state != "resolved" || got.Version != first.Version {
		t.Fatalf("stale observation updated state: %q, %+v, %v", state, got, err)
	}
}

func TestTimelineAndAuditFailureRollBackIncidentMutation(t *testing.T) {
	f := newFixture(t)
	i, _ := createFixture(t, f, "prod", "before")
	if _, err := f.db.Exec("DROP TABLE operation_audit"); err != nil {
		t.Fatal(err)
	}
	path := "/incidents/" + i.ID
	decoded[map[string]any](t, f.request("PATCH", path, "admin", `{"status":"investigating","reason":"auditable"}`, "", `"1"`), 503)
	unchanged := decoded[Detail](t, f.request("GET", path, "admin", "", "", ""), 200)
	if unchanged.Version != 1 || unchanged.Incident.Status != Pending {
		t.Fatalf("update escaped failed audit transaction: %+v", unchanged)
	}
	timeline := decoded[Page[TimelineEntry]](t, f.request("GET", path+"/timeline", "admin", "", "", ""), 200)
	if len(timeline.Items) != 1 || timeline.Items[0].Kind != "created" {
		t.Fatalf("timeline escaped failed audit transaction: %+v", timeline)
	}
}
