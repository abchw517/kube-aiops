package holmes

import (
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
