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
	request(environmentKind, http.MethodPost, `{"name":" "}`, 400)
	request(environmentKind, http.MethodPut, `{"id":"development","name":"开发"}`, 200)
	request(environmentKind, http.MethodPost, `{"name":"研发"}`, 409)
	request(environmentKind, http.MethodDelete, `{"id":"staging"}`, 204)

	w := request(businessKind, http.MethodPost, `{"name":"平台"}`, 200)
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
