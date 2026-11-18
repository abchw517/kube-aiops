package portal

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
)

func TestClusterSnapshotIsolationAndReplacement(t *testing.T) {
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "clusters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := &httpapi.ClusterBackends{Backend: testBackend{}}
	o, err := newOnboarding(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	add := func(id string) string {
		t.Helper()
		w := httptest.NewRecorder()
		o.admin(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"id":"`+id+`","name":"instance","environment":"test","business":"云帆","group":"apps","mode":"push","enabled":true}`)))
		if w.Code != 200 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		return strings.TrimSuffix(strings.TrimPrefix(w.Body.String(), `{"token":"`), "\"}\n")
	}
	a, b := add("a"), add("b")
	now := time.Now().UTC()
	report := func(id, token, body string, captured time.Time, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/v1/collector/snapshot?cluster="+id, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Snapshot-Captured-At", captured.Format(time.RFC3339Nano))
		w := httptest.NewRecorder()
		o.report(w, r)
		if w.Code != want {
			t.Fatalf("report %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	const item = `[{"id":"same","cluster":"spoofed","problem":"image error","severity":"warning","source":"k8sgpt","resource":{"kind":"Pod","name":"app"}}]`
	report("a", b, item, now, 401)
	lock := func(want bool) {
		t.Helper()
		state, err := o.readCollection(t.Context(), "a")
		if err != nil || (state.FirstAt > 0) != want {
			t.Fatalf("first collection: %+v, %v", state, err)
		}
	}
	report("a", a, "[]", now.Add(-time.Second), 204)
	lock(false)
	report("a", a, item, now, 204)
	report("b", b, item, now, 204)
	lock(true)
	report("a", a, "[]", now.Add(-time.Second), 409)
	at, _ := registry.Lookup("a")
	bt, _ := registry.Lookup("b")
	itemB, err := bt.Backend.GetFinding(t.Context(), "same")
	if err != nil || itemB.Cluster != "b" {
		t.Fatalf("wrong cluster: %+v %v", itemB, err)
	}
	report("a", a, "[]", now.Add(time.Second), 204)
	lock(true)
	if _, err := at.Backend.GetFinding(t.Context(), "same"); err == nil {
		t.Fatal("snapshot did not remove old result")
	}
	if _, err := bt.Backend.GetFinding(t.Context(), "same"); err != nil {
		t.Fatal(err)
	}
}

func TestManagedAIReviewUsesOnlySelectedPullInstance(t *testing.T) {
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "clusters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := &httpapi.ClusterBackends{Backend: testBackend{}}
	o := &onboarding{store: store, registry: registry}
	spec := managedCluster{ID: "remote", Name: "remote", Environment: "test", Business: "ops", Group: "apps",
		Mode: "pull", Enabled: true, AIEnabled: true, APIURL: "https://remote.example",
		EngineNamespace: "engine-ns", EngineName: "remote-engine"}
	target, err := o.build(spec)
	if err != nil {
		t.Fatal(err)
	}
	registry.SetManaged(spec.ID, target)
	if review := o.review("remote"); !review.Enabled() {
		t.Fatal("remote Engine resolver was not attached")
	}
	if review := o.review("other"); review != nil {
		t.Fatal("missing instance must not fall back to another Engine")
	}
	spec.Mode = "push"
	if _, err := o.build(spec); err == nil {
		t.Fatal("push-only cluster unexpectedly enabled Kubernetes Engine reading")
	}
}
