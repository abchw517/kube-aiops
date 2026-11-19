package diagnosis

import (
	"context"
	"encoding/json"
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

type reviewFixtureBackend struct{ httpapi.Backend }

func (reviewFixtureBackend) GetFinding(context.Context, string) (finding.Finding, error) {
	return finding.Finding{ID: "finding-1", Cluster: "local", Namespace: "dev", Severity: "warning", Source: "k8sgpt",
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
		adminhttp.WriteJSON(w, map[string]any{"choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]string{"content": "更新后的诊断结论"},
		}}})
	}))
	defer provider.Close()
	t.Setenv("FINDING_AI_ENGINE_ENABLED", "true")
	review, err := New(reviewFixtureBackend{}, store, func(context.Context) (kubernetes.EngineAIConfig, error) {
		return kubernetes.EngineAIConfig{Endpoint: provider.URL, Model: "fixture", Key: "fixture-key"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	review.client = provider.Client()
	request := func(method, body string, status int) string {
		t.Helper()
		out := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/admin/finding-analysis?id=finding-1", strings.NewReader(body))
		review.ServeHTTP(out, req.WithContext(reviewPublishContext(req.Context(), review.store, true)))
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
	<-prompts // Discard the prompt for the provider failure.
	review.correlator = correlation.CorrelatorFunc(func(_ context.Context, req correlation.Request) (correlation.CorrelationBundle, error) {
		if req.FindingID != "finding-1" || req.Scope.Cluster != "local" || req.Scope.Resource.Name != "worker" {
			t.Errorf("unexpected evidence scope: %+v", req)
		}
		return correlation.CorrelationBundle{
			FindingID: req.FindingID, Scope: req.Scope,
			Window: correlation.TimeWindow{Start: "2026-09-07T01:30:00Z", End: "2026-09-07T02:00:00Z"},
			Budget: correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 50},
			Sources: []correlation.SourceStatus{
				{Source: correlation.SourceKubernetesEvents, State: correlation.SourceDisabled},
				{Source: correlation.SourcePrometheus, State: correlation.SourceDisabled},
				{Source: correlation.SourceVictoriaLogs, State: correlation.SourceAvailable},
				{Source: correlation.SourceAlertmanager, State: correlation.SourceDisabled},
			},
			Signals: []correlation.CorrelationSignal{{
				ID: "log-1", Source: correlation.SourceVictoriaLogs, Type: correlation.SignalTypeLog,
				Fingerprint: "sha256:abc123", Summary: "registry returned 403 Authorization: Bearer fixture-sensitive-token",
			}},
		}, nil
	})
	if reply := request("POST", `{"action":"chat","revision":2,"messages":[{"role":"user","text":"check pulling evidence"}]}`, 200); !strings.Contains(reply, `"reply"`) {
		t.Fatal(reply)
	}
	if prompt := <-prompts; !strings.Contains(prompt, "registry returned 403") || strings.Contains(prompt, "fixture-sensitive-token") {
		t.Fatal(prompt)
	}
	if history := request("GET", "", 200); strings.Contains(history, `"revision":3`) {
		t.Fatal(history)
	}
	messages := `[{"role":"user","text":"check pulling evidence"},{"role":"assistant","text":"更新后的诊断结论"}]`
	body := `{"action":"finalize","revision":2,"messages":` + messages + `}`
	request("POST", body, 200)
	if prompt := <-prompts; !strings.Contains(prompt, "check pulling evidence") || !strings.Contains(prompt, "registry returned 403") {
		t.Fatal(prompt)
	}
	request("POST", body, 409)
	if history := request("GET", "", 200); !strings.Contains(history, `"revision":3`) {
		t.Fatal(history)
	}

	draftResponse := request("POST", `{"action":"summarize","revision":3,"messages":`+messages+`}`, 200)
	<-prompts
	var draft struct{ DraftID string }
	if err := json.Unmarshal([]byte(draftResponse), &draft); err != nil || len(draft.DraftID) != 64 {
		t.Fatalf("invalid draft response: %s", draftResponse)
	}
	var count int
	if err := store.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM wiki_documents").Scan(&count); err != nil || count != 0 {
		t.Fatal("summarizing published Wiki content before confirmation")
	}
	confirmation := `{"action":"confirm","revision":3,"draftId":"` + draft.DraftID + `"}`
	result := request("POST", confirmation, 200)
	if !strings.Contains(result, `"knowledgeId"`) || !strings.Contains(result, `"revision":4`) {
		t.Fatal(result)
	}
	request("POST", confirmation, 409)
	if err := store.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM wiki_documents").Scan(&count); err != nil || count != 1 {
		t.Fatal("expected exactly one confirmed Wiki document")
	}

}

func reviewPublishContext(ctx context.Context, store *identitystore.Store, allowed bool) context.Context {
	authorizer := authorization.AuthorizerFunc(func(_ context.Context, req authorization.DecisionRequest) (authorization.Decision, error) {
		return authorization.Decision{Allowed: allowed && req.Capability == authorization.CapabilityWikiPublish &&
			req.Scope.Cluster == "local" && req.Scope.Namespace == "dev"}, nil
	})
	return wiki.New(store, authorizer, "").WithFindingPublish(ctx, identity.Principal{})
}

func TestEvidenceAssessmentAndIndependentPublication(t *testing.T) {
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "identities.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	providerCalls := 0
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		var req struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 2 {
			t.Error("model did not get structured input")
			w.WriteHeader(400)
			return
		}
		output := `{"state":"supported","judgment":"镜像拉取异常","references":["e1","unknown","original"],"gaps":"最新事件","next":"查看事件"}`
		if strings.Contains(req.Messages[1].Content, "原始故障已恢复") {
			output = "not a JSON response"
		}
		adminhttp.WriteJSON(w, map[string]any{"choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]string{"content": output},
		}}})
	}))
	defer provider.Close()
	review, err := New(reviewFixtureBackend{}, store, func(context.Context) (kubernetes.EngineAIConfig, error) {
		return kubernetes.EngineAIConfig{Endpoint: provider.URL, Model: "fixture", Key: "fixture"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	review.client = provider.Client()
	request := func(method, body string, allow bool, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/admin/finding-analysis?id=finding-1", strings.NewReader(body))
		out := httptest.NewRecorder()
		review.ServeHTTP(out, req.WithContext(reviewPublishContext(req.Context(), store, allow)))
		if out.Code != want {
			t.Fatalf("%s: got %d, want %d: %s", body, out.Code, want, out.Body.String())
		}
		return out.Body.String()
	}
	request("GET", "", true, 200)
	if providerCalls != 0 {
		t.Fatal("model called while loading review history")
	}
	if result := request("POST", `{"action":"assess","revision":0}`, true, 200); !strings.Contains(result, `"state":"insufficient"`) || strings.Contains(result, "unknown") {
		t.Fatal(result)
	}
	review.SetCorrelator(correlation.CorrelatorFunc(func(_ context.Context, req correlation.Request) (correlation.CorrelationBundle, error) {
		now := time.Now().UTC()
		return correlation.CorrelationBundle{
			FindingID: req.FindingID, Scope: req.Scope,
			Window: correlation.TimeWindow{Start: now.Add(-time.Minute).Format(time.RFC3339), End: now.Format(time.RFC3339)},
			Budget: correlation.CorrelationBudget{WindowSeconds: 60, PerSourceTimeoutMillis: 1000, MaxSignalsPerSource: 4},
			Sources: []correlation.SourceStatus{
				{Source: correlation.SourceKubernetesEvents, State: correlation.SourceAvailable},
				{Source: correlation.SourcePrometheus, State: correlation.SourceUnavailable},
				{Source: correlation.SourceVictoriaLogs, State: correlation.SourceDisabled},
				{Source: correlation.SourceAlertmanager, State: correlation.SourceDisabled},
			},
			Signals: []correlation.CorrelationSignal{{ID: "event-1", Source: correlation.SourceKubernetesEvents,
				Type: correlation.SignalTypeEvent, Name: "Failed", LastSeen: now.Format(time.RFC3339)}},
		}, nil
	}))
	if result := request("POST", `{"action":"assess","revision":0}`, true, 200); !strings.Contains(result, `"state":"supported"`) || !strings.Contains(result, `"signalId":"event-1"`) || strings.Contains(result, "unknown") {
		t.Fatal(result)
	}
	request("POST", `{"action":"assess","revision":0,"observation":"原始故障已恢复"}`, true, 502)
	checkRows := func(reviews, documents int) {
		t.Helper()
		var r, d int
		err := store.QueryRowContext(t.Context(), "SELECT (SELECT COUNT(*) FROM finding_reviews), (SELECT COUNT(*) FROM wiki_documents)").Scan(&r, &d)
		if err != nil || r != reviews || d != documents {
			t.Fatalf("got %d reviews and %d documents, want %d and %d: %v", r, d, reviews, documents, err)
		}
	}
	checkRows(0, 0)
	request("POST", `{"action":"save","revision":0,"observation":"值班员已确认镜像仓库故障"}`, false, 200)
	request("POST", `{"action":"save","revision":0,"observation":"来自旧版本"}`, true, 409)
	checkRows(1, 0)
	finding, err := review.GetFinding(t.Context(), "finding-1")
	if err != nil || finding.ReviewRevision != 1 || finding.OriginalDetails == nil || *finding.OriginalDetails != "原始诊断" {
		t.Fatalf("original explanation or saved revision missing: %+v, %v", finding, err)
	}
	request("POST", `{"action":"preview-publication","revision":1}`, false, 403)
	raw := request("POST", `{"action":"preview-publication","revision":1}`, true, 200)
	var preview struct{ PreviewID string }
	if err := json.Unmarshal([]byte(raw), &preview); err != nil || preview.PreviewID == "" || !strings.Contains(raw, "值班员已确认") {
		t.Fatal(raw)
	}
	checkRows(1, 0)
	request("POST", `{"action":"publish","revision":1,"previewId":"old"}`, true, 409)
	publish := `{"action":"publish","revision":1,"previewId":"` + preview.PreviewID + `"}`
	request("POST", publish, false, 403)
	first := request("POST", publish, true, 200)
	if repeat := request("POST", publish, true, 200); first != repeat {
		t.Fatal("repeated publication changed the document")
	}
	checkRows(1, 1)
	request("POST", `{"action":"save","revision":1,"observation":"复核修订"}`, true, 200)
	request("POST", publish, true, 409)
	checkRows(2, 1)
	if providerCalls != 3 {
		t.Fatalf("expected only three model calls, got %d", providerCalls)
	}
}
