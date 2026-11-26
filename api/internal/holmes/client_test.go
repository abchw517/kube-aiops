package holmes

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExactScopeAndResponse(t *testing.T) {
	scope := Scope{Source: "zabbix", SourceID: "monitor-a", Environment: "test", Business: "ops"}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/chat" || r.Header.Get("X-API-Key") != "fixture" {
			t.Error("wrong request")
		}
		json.NewEncoder(w).Encode(map[string]string{"analysis": "需要验证消费组速率"})
	}))
	defer server.Close()
	raw, _ := json.Marshal([]binding{{Scope: scope, Endpoint: server.URL, APIKey: "fixture"}})
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOLMES_BINDINGS_FILE", path)
	got, err := Investigate(t.Context(), scope, "lag")
	if err != nil || got != "需要验证消费组速率" {
		t.Fatal(got, err)
	}
	scope.SourceID = "monitor-b"
	if _, err = Investigate(t.Context(), scope, "lag"); !errors.Is(err, ErrUnconfigured) || calls != 1 {
		t.Fatalf("wrong scope reached engine: %v", err)
	}
}

func TestConfiguredModelProjectionAndReadiness(t *testing.T) {
	scope := Scope{Source: "wiki", SourceID: "published-excerpts"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/holmes/readyz" || r.Header.Get("X-API-Key") != "private-key" {
			t.Error("unexpected readiness request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	data, err := json.Marshal([]binding{{Scope: scope, Endpoint: server.URL + "/holmes", APIKey: "private-key", Model: "accurate"}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOLMES_BINDINGS_FILE", path)
	models, err := ConfiguredModels()
	if err != nil || len(models) != 1 || models[0].Model != "accurate" || models[0].Scope != scope {
		t.Fatalf("unexpected model projection: %+v, %v", models, err)
	}
	view, err := json.Marshal(models)
	if err != nil || bytes.Contains(view, []byte("private-key")) || bytes.Contains(view, []byte("/holmes")) {
		t.Fatalf("unexpected credential or internal route disclosed")
	}
	if err := CheckService(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	scope.Namespace = "other"
	if err := CheckService(t.Context(), scope); !errors.Is(err, ErrUnconfigured) {
		t.Fatalf("unknown scope returned %v", err)
	}
}
