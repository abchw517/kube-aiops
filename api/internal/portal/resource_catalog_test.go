package portal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
)

func TestResourceCatalogLifecycle(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := &httpapi.ClusterBackends{Backend: testBackend{}}
	o, err := newOnboarding(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	request := func(kind, method, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		o.catalog(w, httptest.NewRequest(method, "/", strings.NewReader(body)), kind)
		if w.Code != want {
			t.Fatalf("%s %s: %d != %d: %s", method, kind, w.Code, want, w.Body)
		}
		return w
	}
	for _, id := range []string{"", "Upper", "a_b", "a b", "1sandbox"} {
		request(environmentKind, http.MethodPost, `{"id":"`+id+`","name":"沙盒"}`, 400)
	}
	request(environmentKind, http.MethodPost, `{"id":"sandbox","name":"沙盒"}`, 200)
	request(environmentKind, http.MethodPost, `{"id":"sandbox","name":"另一个沙盒"}`, 409)
	request(businessKind, http.MethodPost, `{"id":"sandbox","name":"业务沙盒"}`, 200)
	request(environmentKind, http.MethodDelete, `{"id":"sandbox"}`, 204)
	request(environmentKind, http.MethodPost, `{"name":" "}`, 400)
	request(environmentKind, http.MethodPut, `{"id":"development","name":"开发"}`, 200)
	request(environmentKind, http.MethodPost, `{"id":"duplicate-name","name":"研发"}`, 409)
	request(environmentKind, http.MethodDelete, `{"id":"staging"}`, 204)

	w := request(businessKind, http.MethodPost, `{"id":"platform","name":"平台"}`, 200)
	var business catalogEntry
	if err := json.Unmarshal(w.Body.Bytes(), &business); err != nil || business.ID == "" {
		t.Fatalf("invalid entry: %s", w.Body)
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO managed_clusters(id,document,token_hash) VALUES(?,?,?)", "remote-a", `{"environment":"研发","business":"平台"}`, ""); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ kind, id string }{{environmentKind, "development"}, {businessKind, business.ID}} {
		w = request(item.kind, http.MethodDelete, `{"id":"`+item.id+`"}`, 409)
		var conflict struct{ References []string }
		if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil || len(conflict.References) != 1 || conflict.References[0] != "remote-a" {
			t.Fatalf("expected cluster reference: %s", w.Body)
		}
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO resource_catalog(kind,id,name) VALUES(?,?,?)", businessKind, "Legacy_ID-7", "旧业务"); err != nil {
		t.Fatal(err)
	}
	request(businessKind, http.MethodPut, `{"id":"Legacy_ID-7","name":"旧业务改名"}`, 200)
	request(businessKind, http.MethodDelete, `{"id":"Legacy_ID-7"}`, 204)
	if err := initResourceCatalog(ctx, store); err != nil {
		t.Fatal(err)
	}
	entries, err := readCatalog(ctx, store, environmentKind)
	if err != nil || len(entries) != 3 {
		t.Fatalf("preset recreated after initialization: %+v, %v", entries, err)
	}
	for _, entry := range entries {
		if entry.ID == "development" && entry.Name != "开发" {
			t.Fatalf("rename failed to retain preset ID: %+v", entry)
		}
	}
	if _, err := store.ExecContext(ctx, "DELETE FROM managed_clusters WHERE id=?", "remote-a"); err != nil {
		t.Fatal(err)
	}
	request(businessKind, http.MethodDelete, `{"id":"`+business.ID+`"}`, 204)
	request(businessKind, http.MethodDelete, `{"id":"`+business.ID+`"}`, 404)
}

func TestCatalogIDChangesPreserveReferencesAndValidateConflicts(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "rename.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	o, err := newOnboarding(store, &httpapi.ClusterBackends{Backend: testBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(kind, method, body string, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		o.catalog(w, httptest.NewRequest(method, "/", strings.NewReader(body)), kind)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", kind, method, w.Code, want, w.Body)
		}
		return w.Body.String()
	}
	call(environmentKind, "PUT", `{"originalId":"development","id":"testing","name":"研发"}`, 409)
	call(environmentKind, "PUT", `{"originalId":"development","id":"Dev","name":"研发"}`, 400)
	call(environmentKind, "PUT", `{"originalId":"development","id":"new-dev","name":"研发"}`, 200)
	call(environmentKind, "POST", `{"id":"development","name":"另一环境"}`, 409)
	call(environmentKind, "PUT", `{"originalId":"new-dev","id":"development","name":"研发"}`, 200)
	call(businessKind, "POST", `{"id":"yunfan","name":"云帆"}`, 200)
	if _, err := store.ExecContext(ctx, "INSERT INTO namespace_business VALUES ('remote','platform','yunfan')"); err != nil {
		t.Fatal(err)
	}
	call(businessKind, "PUT", `{"originalId":"yunfan","id":"yunfan-app","name":"云帆"}`, 200)
	for _, id := range []string{"yunfan", "yunfan-app"} {
		allowed, err := o.namespaceBusiness(ctx, "remote", "platform", id)
		if err != nil || !allowed {
			t.Fatalf("%s lost reference: %v, %v", id, allowed, err)
		}
	}
	if result := call(businessKind, "DELETE", `{"id":"yunfan-app"}`, 409); !strings.Contains(result, "remote/platform") {
		t.Fatal(result)
	}
	call(businessKind, "PUT", `{"originalId":"yunfan","id":"stale-write","name":"云帆"}`, 404)
}

func TestDedicatedClusterOnlyAllowsItsOwnBusiness(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "dedicated.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	o, err := newOnboarding(store, &httpapi.ClusterBackends{Backend: testBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	request := func(handler http.HandlerFunc, method, path, body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d %s", method, path, w.Code, want, w.Body)
		}
	}
	catalog := func(w http.ResponseWriter, r *http.Request) { o.catalog(w, r, businessKind) }
	request(catalog, "POST", "/", `{"id":"yunfan","name":"云帆"}`, 200)
	request(catalog, "POST", "/", `{"id":"youshu","name":"有数"}`, 200)
	request(o.admin, "POST", "/", `{"id":"dedicated","name":"独立集群","environment":"生产","business":"云帆","group":"apps","mode":"push","enabled":true,"clusterType":"dedicated"}`, 200)
	if _, err := store.ExecContext(ctx, `UPDATE managed_clusters SET snapshot='[{"namespace":"shared-app"}]' WHERE id='dedicated'`); err != nil {
		t.Fatal(err)
	}
	request(o.namespaceAssignments, "PUT", "/?cluster=dedicated", `{"namespace":"shared-app","businesses":["yunfan"]}`, 204)
	request(o.namespaceAssignments, "PUT", "/?cluster=dedicated", `{"namespace":"shared-app","businesses":["youshu"]}`, 409)
	has, err := o.namespaceBusiness(ctx, "dedicated", "shared-app", "yunfan")
	if err != nil || !has {
		t.Fatalf("failed assignment changed valid business: %v %v", has, err)
	}
}
