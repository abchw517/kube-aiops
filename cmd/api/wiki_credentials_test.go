package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/identitystore"
)

func TestWikiWithPortalTestAccounts(t *testing.T) {
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "true")
	t.Setenv("AUTH_ORIGIN", "https://portal.example.com")
	writeIdentityTestFile(t, "WIKI_CREDENTIAL_ENCRYPTION_KEY_FILE", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))

	dbPath := filepath.Join(t.TempDir(), "identity.db")
	store, err := identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	admin, err := newIdentityAdmin(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := identity.NewSessions(admin.origin, map[string]identity.PasswordVerifier{"local": admin.local})
	if err != nil {
		t.Fatal(err)
	}
	handler := sessions.Wrap(admin.wrap(sessions, http.NotFoundHandler(), nil, nil))
	request := func(method, path, body string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", admin.origin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	login := func(username, password string) *http.Cookie {
		t.Helper()
		return request("POST", "/api/v1/auth/login", `{"provider":"local","username":"`+username+`","password":"`+password+`"}`, nil, 204).Result().Cookies()[0]
	}
	initial := login("admin", "admin123")
	request("POST", "/api/v1/admin/password", `{"current":"admin123","password":"fixture-admin-new"}`, initial, 204)
	root := login("admin", "fixture-admin-new")
	const source = "/api/v1/wiki/sources/ops"
	const credential = source + "/credential"
	request("PUT", source, `{"url":"https://wiki.example.com/ops","cluster":"local","namespace":"dev"}`, root, 204)
	request("PUT", source, `{"url":"https://wiki.example.com/changed","cluster":"local","namespace":"dev"}`, root, 409)
	request("POST", "/api/v1/admin/users", `{"username":"alice","password":"fixture-alice-pass","groups":["ops"]}`, root, 204)
	request("POST", "/api/v1/admin/users", `{"username":"bob","password":"fixture-bob-pass","groups":["ops"]}`, root, 204)
	alice := login("alice", "fixture-alice-pass")
	bob := login("bob", "fixture-bob-pass")
	request("GET", credential, "", nil, 401)
	request("GET", credential, "", alice, 404)
	request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[{"effect":"allow","groups":["local:ops"],"capabilities":["wiki:sync"],"scopes":[{"cluster":"local","namespace":"dev"}]}]}`, root, 204)
	const wikiPassword = "fixture-alice-wiki-secret"
	request("PUT", credential, `{"username":"wiki-alice","password":"`+wikiPassword+`"}`, alice, 204)
	request("POST", "/api/v1/auth/logout", "", alice, 204)
	request("GET", credential, "", alice, 401)
	alice = login("alice", "fixture-alice-pass")
	result := request("GET", credential, "", alice, 200)
	if strings.TrimSpace(result.Body.String()) != `{"username":"wiki-alice","configured":true}` || result.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected credential response: %s", result.Body)
	}
	if result := request("GET", credential, "", bob, 200); strings.TrimSpace(result.Body.String()) != `{"configured":false}` {
		t.Fatalf("Bob received another user's binding: %s", result.Body)
	}
	request("PUT", credential, `{"username":"wiki-bob","password":"fixture-bob-wiki-secret"}`, bob, 204)
	request("DELETE", credential, "", bob, 204)

	// A fresh database handle can decrypt Alice's binding for a future sync job.
	reopened, err := identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := &identityAdmin{store: reopened}
	aliceCredential, err := restarted.loadWikiCredential(t.Context(), "ops", identity.Principal{Provider: "local", Subject: "local:alice"})
	if err != nil || aliceCredential != (wikiCredential{Username: "wiki-alice", Password: wikiPassword}) {
		t.Fatal("saved credential did not survive database reopen")
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil || bytes.Contains(raw, []byte(wikiPassword)) {
		t.Fatal("database read failed or database contains the cleartext password")
	}
	t.Run("publishes sanitized wiki documents and isolates scope", func(t *testing.T) {
		const knowledge = "/api/v1/wiki/knowledge"
		const imports = "/api/v1/wiki/imports"
		request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[{"effect":"allow","subjects":["local:alice"],"capabilities":["wiki:read","wiki:publish"],"scopes":[{"cluster":"local","namespace":"dev"}]}]}`, root, 204)
		input := `{"document":{"cluster":"local","namespace":"dev","environment":"测试","business":"运维","body":"# 镜像拉取\npassword=fixture-password-value"},"publish":false}`
		preview := request("POST", imports, input, alice, 200)
		if strings.Contains(preview.Body.String(), "fixture-password-value") || !strings.Contains(preview.Body.String(), `"title":"镜像拉取"`) {
			t.Fatalf("invalid preview: %s", preview.Body)
		}
		if out := request("GET", knowledge, "", alice, 200); !strings.Contains(out.Body.String(), `"total":0`) {
			t.Fatal(out.Body.String())
		}
		publish := strings.Replace(input, "false", "true", 1)
		request("POST", imports, strings.Replace(publish, `"dev"`, `"prod"`, 1), alice, 403)
		request("POST", imports, publish, bob, 403)
		result := request("POST", imports, publish, alice, 201)
		var doc wikiDocument
		if err := json.Unmarshal(result.Body.Bytes(), &doc); err != nil || doc.ID == "" {
			t.Fatalf("invalid publish response: %v", err)
		}
		request("POST", imports, publish, alice, 200)
		if out := request("GET", knowledge+"?documentType="+url.QueryEscape("排障手册"), "", alice, 200); !strings.Contains(out.Body.String(), `"total":1`) || strings.Contains(out.Body.String(), `"body"`) {
			t.Fatal(out.Body.String())
		}
		path := knowledge + "/" + doc.ID + "/revisions/1"
		request("GET", path, "", alice, 200)
		request("GET", path, "", bob, 404)
		if out := request("GET", knowledge, "", bob, 200); strings.Contains(out.Body.String(), "运维") || !strings.Contains(out.Body.String(), `"total":0`) {
			t.Fatal(out.Body.String())
		}
	})

}
