package portal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/holmes"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestAIWorkspacePublishAndSkillReview(t *testing.T) {
	scope := holmes.Scope{Source: "finding", SourceID: "local", Cluster: "local", Namespace: "ops"}
	actor := identity.Principal{Subject: "author"}
	reviewer := identity.Principal{Subject: "reviewer"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/model":
			w.Write([]byte(`{"model_name":["fast","accurate"]}`))
		case "/api/chat":
			var input struct{ Model, Ask string }
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				t.Error("invalid request")
			}
			if input.Model != "accurate" || !strings.Contains(input.Ask, "核对失败事件") {
				t.Errorf("published configuration not applied: %+v", input)
			}
			w.Write([]byte(`{"analysis":"checked"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	raw, err := json.Marshal([]any{struct {
		holmes.Scope
		Endpoint string `json:"endpoint"`
	}{scope, upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "bindings.json")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOLMES_BINDINGS_FILE", file)
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	workspace, err := holmes.OpenWorkspace(store.DB)
	if err != nil {
		t.Fatal(err)
	}
	ctx := holmes.WithWorkspace(t.Context(), workspace, nil)
	request := func(area, action string, body map[string]any, principal identity.Principal, status int) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/admin/"+area+"?action="+action, bytes.NewReader(data)).WithContext(ctx)
		result := httptest.NewRecorder()
		if area == "ai-services" {
			serveAIServices(result, req)
		} else {
			serveAISkills(result, req, nil, principal)
		}
		if result.Code != status {
			t.Fatalf("%s: got %d want %d: %s", action, result.Code, status, result.Body)
		}
		return result
	}
	fields := map[string]any{"source": scope.Source, "sourceId": scope.SourceID, "clusterId": scope.Cluster, "namespace": scope.Namespace}
	cfg := holmes.ModelConfig{Name: "目标集群", Provider: "vLLM", Fast: "fast", Accurate: "accurate"}
	fields["config"] = cfg
	request("ai-services", "save", fields, actor, 200)
	fields["version"] = 1
	request("ai-services", "publish", fields, actor, 200)
	cfg.Accurate = "missing"
	fields["config"], fields["version"] = cfg, 2
	request("ai-services", "save", fields, actor, 200)
	fields["version"] = 3
	request("ai-services", "publish", fields, actor, 409)
	result := request("ai-skills", "upload", map[string]any{"scope": scope, "name": "Pod 异常", "keyword": "pod", "body": "核对失败事件"}, actor, 200)
	var skill holmes.Entry[holmes.Skill]
	if json.Unmarshal(result.Body.Bytes(), &skill) != nil {
		t.Fatal("missing skill")
	}
	approval := map[string]any{"id": skill.ID, "version": skill.Version}
	request("ai-skills", "approve", approval, actor, 403)
	request("ai-skills", "approve", approval, reviewer, 200)
	trace, applied := holmes.TraceSkills(ctx)
	if _, err = holmes.Chat(trace, scope, "read", map[string]string{"issue": "pod"}, holmes.AssessmentFormat()); err != nil {
		t.Fatal(err)
	}
	if len(*applied) != 1 || (*applied)[0] != "Pod 异常" {
		t.Fatalf("applied skills: %v", *applied)
	}
	requested, applied := holmes.TraceSkills(holmes.WithSkillRequest(ctx, "使用 Pod 异常 技能"))
	if _, err = holmes.Chat(requested, scope, "read", map[string]string{"issue": "container"}, holmes.AssessmentFormat()); err != nil || len(*applied) != 1 {
		t.Fatalf("named skill invocation: %v %v", *applied, err)
	}
	entry, err := holmes.GetEntry[holmes.Connection](ctx, workspace, "connection", holmes.ScopeID(scope))
	if err != nil || entry.Data.Effective.Accurate != "accurate" || entry.Data.Desired.Accurate != "missing" {
		t.Fatalf("failed release changed live config: %+v %v", entry, err)
	}
}
