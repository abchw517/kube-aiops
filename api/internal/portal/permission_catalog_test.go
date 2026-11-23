package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
)

func TestPermissionCatalogAndExactWikiActions(t *testing.T) {
	const origin = "https://portal.example"
	t.Setenv("AUTH_ORIGIN", origin)
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "false")
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "permissions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Import(t.Context(), nil, []byte(`{"version":"v1","rules":[]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(t.Context(), `UPDATE platform_identity SET document=? WHERE id=1`,
		`{"accounts":{
		    "ldap:uid=editor,dc=example":{"id":"ldap:uid=editor,dc=example","username":"editor","kind":"ldap","generation":"v1","roles":["editor"]},
		    "ldap:uid=reader,dc=example":{"id":"ldap:uid=reader,dc=example","username":"reader","kind":"ldap","generation":"v1"}
		  },
		  "roles":{"editor":{"id":"editor","name":"编辑者","grants":[
		    {"code":"system.catalog.read","scope":{}},{"code":"system.role.list","scope":{}},{"code":"system.audit.list","scope":{}},
		    {"code":"knowledge.candidate.read","scope":{"environment":"testing","cluster":"test","namespace":"pay"}},
		    {"code":"knowledge.candidate.save","scope":{"environment":"testing","cluster":"test","namespace":"pay"}}
		  ]}},"groups":{}}`); err != nil {
		t.Fatal(err)
	}
	admin, err := permissions.New(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	admin.SetScopeResolvers(func(_ context.Context, id string) (string, error) {
		if id == "prod" {
			return "production", nil
		}
		return "testing", nil
	}, nil)
	sessions, err := identity.NewSessions(origin, map[string]identity.PasswordVerifier{
		"ldap": func(_ context.Context, user, _ string) (identity.Principal, error) {
			if user != "editor" && user != "reader" {
				return identity.Principal{}, identity.ErrUnauthenticated
			}
			return identity.Principal{Subject: "ldap:uid=" + user + ",dc=example", Provider: "platform-ldap", SessionVersion: "v1"}, nil
		},
		"local": func(context.Context, string, string) (identity.Principal, error) {
			return identity.Principal{Subject: "local:admin", Provider: permissions.AdminProvider}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions.ValidateSession = admin.ValidateSession
	wikis := wiki.New(store, admin, origin)
	handler := sessions.Wrap(wrapAdmin(admin, wikis, sessions, http.NotFoundHandler(), nil, nil))
	call := func(method, path, body string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: %d, want %d: %s", method, path, w.Code, status, w.Body)
		}
		return w
	}
	call("GET", "/api/v1/admin/permission-catalog", "", nil, 401)
	login := call("POST", "/api/v1/auth/login", `{"provider":"ldap","username":"editor","password":"fixture-password"}`, nil, 204)
	cookie := login.Result().Cookies()[0]
	result := call("GET", "/api/v1/admin/permission-catalog", "", cookie, 200)
	if !strings.Contains(result.Body.String(), `"resources": "资源管理"`) || !strings.Contains(result.Body.String(), `"system": "系统管理"`) {
		t.Fatal("catalog module split missing")
	}
	result = call("GET", "/api/v1/admin/access", "", cookie, 200)
	if !strings.Contains(result.Body.String(), `"roles"`) || strings.Contains(result.Body.String(), `"groups"`) {
		t.Fatalf("access response leaked ungranted fields: %s", result.Body)
	}
	result = call("GET", "/api/v1/admin/operations", "", cookie, 200)
	if strings.Contains(result.Body.String(), `"retention"`) {
		t.Fatal("audit list must not expose retention configuration")
	}
	call("GET", "/api/v1/admin/operations?record=ABCDEFGHIJKLMNOPQRSTUVWXY2", "", cookie, 403)
	call("PUT", "/api/v1/admin/operations", `{"low":1,"medium":5,"high":9}`, cookie, 403)
	call("POST", "/api/v1/admin/access", `{"action":"role-save","name":"unexpected"}`, cookie, 403)
	call("POST", "/api/v1/wiki/ask", `{"question":"why?"}`, cookie, 403)

	for _, fixture := range []struct{ id, cluster string }{{"test-draft", "test"}, {"prod-draft", "prod"}} {
		doc, _ := json.Marshal(map[string]string{"cluster": fixture.cluster, "namespace": "pay", "title": "旧草稿", "documentType": "排障手册"})
		if _, err := store.ExecContext(t.Context(), `INSERT INTO wiki_candidates(id,knowledge_id,owner,status,version,base_revision,document,reason,updated_at)
			VALUES(?,?,'fixture','draft',1,0,?,'',0)`, fixture.id, fixture.id, string(doc)); err != nil {
			t.Fatal(err)
		}
	}
	edit := `{"action":"save","version":1,"document":{"cluster":"test","namespace":"pay","title":"更新后的手册","documentType":"排障手册"}}`
	call("GET", "/api/v1/wiki/candidates/test-draft", "", cookie, 200)
	call("GET", "/api/v1/wiki/candidates/prod-draft", "", cookie, 404)
	call("POST", "/api/v1/wiki/candidates/prod-draft", edit, cookie, 404)
	call("POST", "/api/v1/wiki/candidates/test-draft", `{"action":"publish","version":1}`, cookie, 403)
	call("POST", "/api/v1/wiki/candidates/test-draft", `{"action":"discard","version":1}`, cookie, 403)
	call("POST", "/api/v1/wiki/candidates/test-draft", `{"action":"unknown","version":1}`, cookie, 400)
	call("POST", "/api/v1/wiki/candidates/test-draft", edit, cookie, 200)

	adminCookie := call("POST", "/api/v1/auth/login", `{"provider":"local","username":"admin","password":"fixture-password"}`, nil, 204).Result().Cookies()[0]
	reader := call("POST", "/api/v1/auth/login", `{"provider":"ldap","username":"reader","password":"fixture-password"}`, nil, 204).Result().Cookies()[0]
	change := func(path string, body map[string]any) string {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response := call("POST", path, string(payload), adminCookie, 200)
		var entries []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil || len(entries) != 1 || entries[0].ID == "" || entries[0].Reason != "" {
			t.Fatalf("change failed: %v, %s", err, response.Body)
		}
		return entries[0].ID
	}
	const accessPath, accountsPath = "/api/v1/admin/access", "/api/v1/admin/accounts"
	const testDraft, prodDraft = "/api/v1/wiki/candidates/test-draft", "/api/v1/wiki/candidates/prod-draft"
	const readerID = "ldap:uid=reader,dc=example"
	call("GET", testDraft, "", reader, 404)
	roleID := change(accessPath, map[string]any{
		"action": "role-save", "name": "测试知识草稿只读",
		"grants": json.RawMessage(`[{"code":"knowledge.candidate.read","scope":{"environment":"testing","cluster":"test","namespace":"pay"}}]`),
	})
	call("GET", testDraft, "", reader, 404)
	change(accountsPath, map[string]any{"action": "grant", "id": readerID, "values": []string{roleID}})
	call("GET", testDraft, "", reader, 200)
	call("GET", prodDraft, "", reader, 404)
	call("POST", testDraft, edit, reader, 403)
	groupID := change(accessPath, map[string]any{"action": "group-save", "name": "测试知识库读者", "roleIds": []string{roleID}})
	change(accountsPath, map[string]any{"action": "join", "id": readerID, "values": []string{groupID}})
	change(accountsPath, map[string]any{"action": "revoke", "id": readerID, "values": []string{roleID}})
	call("GET", testDraft, "", reader, 200)
	change(accountsPath, map[string]any{"action": "leave", "id": readerID, "values": []string{groupID}})
	call("GET", testDraft, "", reader, 404)
}
