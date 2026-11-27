package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
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
			serveAIServices(result, req, nil)
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

func TestPortalHolmesBindingWithoutFile(t *testing.T) {
	t.Setenv("HOLMES_BINDINGS_FILE", "")
	t.Setenv("HOLMES_PILOT_API_KEY", "internal-auth")
	t.Setenv("HOLMES_WIKI_ENDPOINT", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "internal-auth" {
			http.Error(w, "fixture sensitive body", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/api/model":
			_, _ = w.Write([]byte(`{"model_name":"[\"accurate-model\"]"}`))
		case "/api/chat":
			var input struct{ Model string }
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Model != "accurate-model" {
				t.Errorf("unexpected model request: %+v", input)
			}
			_, _ = w.Write([]byte(`{"analysis":"checked"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	t.Setenv("HOLMES_PILOT_ENDPOINT", upstream.URL)
	dbPath := filepath.Join(t.TempDir(), "workspace.db")
	store, err := identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	workspace, err := holmes.OpenWorkspace(store.DB)
	if err != nil {
		t.Fatal(err)
	}
	ctx := holmes.WithWorkspace(t.Context(), workspace, nil)
	scope := holmes.Scope{Source: "alertmanager", SourceID: "am-prod", Environment: "prod", Business: "orders", Cluster: "sh-01", Namespace: "orders"}
	validate := func(_ context.Context, received holmes.Scope) error {
		if received != scope {
			return incident.ErrBadScope
		}
		return nil
	}
	call := func(method, action, body string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/admin/ai-services?action="+action, strings.NewReader(body)).WithContext(ctx)
		response := httptest.NewRecorder()
		serveAIServices(response, req, validate)
		if response.Code != want {
			t.Fatalf("%s: got %d want %d: %s", action, response.Code, want, response.Body)
		}
		return response.Body.String()
	}
	read := call("GET", "", "", 200)
	if !strings.Contains(read, `"status":"unconfigured"`) || !strings.Contains(read, `"status":"ready"`) || strings.Contains(read, "internal-auth") {
		t.Fatalf("service readiness should be visible independently: %s", read)
	}
	t.Setenv("HOLMES_PILOT_API_KEY", "")
	failure := call("POST", "discover-binding", `{"source":"alertmanager"}`, 503)
	if !strings.Contains(failure, "HTTP 401") || strings.Contains(failure, "fixture sensitive body") {
		t.Fatalf("expected a safe authentication error: %s", failure)
	}
	t.Setenv("HOLMES_PILOT_API_KEY", "internal-auth")
	call("POST", "discover-binding", `{"source":"alertmanager"}`, 200)
	call("POST", "discover-binding", `{"source":"wiki"}`, 409)
	body := `{"source":"alertmanager","sourceId":"am-prod","environmentId":"prod","businessId":"orders","clusterId":"sh-01","namespace":"orders","model":"accurate-model"}`
	call("POST", "bind", strings.Replace(body, "sh-01", "sh-02", 1), 409)
	call("POST", "bind", strings.Replace(body, "accurate-model", "unknown", 1), 409)
	call("POST", "bind", strings.TrimSuffix(body, "}")+`,"endpoint":"http://example.invalid"}`, 400)
	call("POST", "bind", body, 200)
	call("POST", "bind", body, 409)
	if read := call("GET", "", "", 200); !strings.Contains(read, `"managed":true`) || !strings.Contains(read, `"status":"configured"`) || strings.Contains(read, "internal-auth") {
		t.Fatalf("binding was not returned correctly: %s", read)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = holmes.OpenWorkspace(store.DB)
	if err != nil {
		t.Fatal(err)
	}
	ctx = holmes.WithWorkspace(t.Context(), workspace, nil)
	got, err := holmes.Investigate(ctx, scope, "evidence")
	if got != "checked" || err != nil {
		t.Fatalf("binding did not survive database reopen: %q, %v", got, err)
	}
	scope.Namespace = "other"
	if _, err := holmes.Investigate(ctx, scope, "evidence"); !errors.Is(err, holmes.ErrUnconfigured) {
		t.Fatalf("unbound namespace should not reach Holmes: %v", err)
	}
}
