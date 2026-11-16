package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/identitystore"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

type reviewFixtureBackend struct{ testBackend }

func (reviewFixtureBackend) GetFinding(context.Context, string) (finding.Finding, error) {
	return finding.Finding{ID: "finding-1", Cluster: "local", Severity: "warning", Source: "k8sgpt",
		Resource: finding.ResourceRef{Kind: "Pod", Name: "worker"}, Problem: "image pull failed", Details: "原始诊断"}, nil
}

func TestFindingReviewPersistsAndUsesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identities.db")
	store, err := identitystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	prompts := make(chan string, 3)
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Messages []struct{ Content string } }
		if r.Header.Get("Authorization") != "Bearer fixture-key" || json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
			w.WriteHeader(400)
			return
		}
		prompt := request.Messages[1].Content
		prompts <- prompt
		if strings.Contains(prompt, "model-error") {
			w.WriteHeader(503)
			return
		}
		writeAdminJSON(w, map[string]any{"choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]string{"content": "更新后的诊断结论"},
		}}})
	}))
	defer provider.Close()
	t.Setenv("FINDING_AI_ENGINE_ENABLED", "true")
	review, err := newFindingReview(reviewFixtureBackend{}, store, func(context.Context) (kubernetes.EngineAIConfig, error) {
		return kubernetes.EngineAIConfig{Endpoint: provider.URL, Model: "fixture", Key: "fixture-key"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	review.client = provider.Client()
	request := func(method, body string, status int) string {
		t.Helper()
		out := httptest.NewRecorder()
		review.serve(out, httptest.NewRequest(method, "/api/v1/admin/finding-analysis?id=finding-1", strings.NewReader(body)))
		if out.Code != status {
			t.Fatalf("expected %d, got %d: %s", status, out.Code, out.Body.String())
		}
		return out.Body.String()
	}
	request("POST", `{"source":"Pod events","observation":"registry rejected request","revision":0}`, 200)
	if first := <-prompts; !strings.Contains(first, "原始诊断") || !strings.Contains(first, "registry rejected request") {
		t.Fatal(first)
	}
	request("POST", `{"source":"Pod events","observation":"registry recovered","revision":0}`, 409)
	request("POST", `{"source":"Pod events","observation":"registry recovered","revision":1}`, 200)
	if second := <-prompts; !strings.Contains(second, "更新后的诊断结论") {
		t.Fatal(second)
	}
	request("POST", `{"source":"Pod events","observation":"model-error","revision":2}`, 502)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = identitystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	review.store = store
	if history := request("GET", "", 200); !strings.Contains(history, `"revision":2`) || strings.Contains(history, `"revision":3`) {
		t.Fatal(history)
	}
	item, err := review.GetFinding(t.Context(), "finding-1")
	if err != nil || item.Details != "AI 复核结论\n\n更新后的诊断结论" {
		t.Fatalf("unexpected finding: %+v, %v", item, err)
	}
}
