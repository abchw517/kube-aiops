package holmes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	models, err := ConfiguredModels(t.Context())
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

func TestChatClassifiesFailuresWithoutUpstreamContent(t *testing.T) {
	const privateBody = "sensitive-upstream-body"
	scope := Scope{Source: "zabbix", SourceID: "monitor-a"}
	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, "HOLMES_CHAT_AUTH_FAILED"},
		{http.StatusForbidden, "HOLMES_CHAT_AUTH_FAILED"},
		{http.StatusNotFound, "HOLMES_CHAT_ROUTE_NOT_FOUND"},
		{http.StatusTooManyRequests, "HOLMES_CHAT_RATE_LIMITED"},
		{http.StatusGatewayTimeout, "HOLMES_CHAT_TIMEOUT"},
		{http.StatusInternalServerError, "HOLMES_CHAT_UPSTREAM_ERROR"},
		{http.StatusOK, "HOLMES_CHAT_INVALID_RESPONSE"},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(privateBody))
			}))
			defer server.Close()
			data, err := json.Marshal([]binding{{Scope: scope, Endpoint: server.URL, APIKey: "private-key"}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "bindings.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOLMES_BINDINGS_FILE", path)
			_, err = Investigate(t.Context(), scope, "alert")
			var failure *ChatError
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &failure) || failure.Code != tc.code || failure.UpstreamStatus != tc.status {
				t.Fatalf("expected %s for upstream HTTP %d, got %v", tc.code, tc.status, err)
			}
			for _, private := range []string{privateBody, "private-key", server.URL} {
				if strings.Contains(failure.Error(), private) {
					t.Fatal("diagnostic error disclosed private upstream data")
				}
			}
		})
	}
}

func TestChatNetworkErrorDistinguishesDeadlineAndCancellation(t *testing.T) {
	var failure *ChatError
	if err := chatNetworkError(context.DeadlineExceeded); !errors.As(err, &failure) || failure.Code != "HOLMES_CHAT_TIMEOUT" {
		t.Fatalf("deadline classified as %v", err)
	}
	if err := chatNetworkError(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation classified as %v", err)
	}
	if err := chatNetworkError(errors.New("test dial failure")); !errors.As(err, &failure) || failure.Code != "HOLMES_CHAT_CONNECT_FAILED" {
		t.Fatalf("dial failure classified as %v", err)
	}
}
