package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/internal/identitystore"
	"github.com/abchw517/kube-aiops/internal/security"
)

func TestManagedIdentityBootstrap(t *testing.T) {
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "true")
	t.Setenv("AUTH_ORIGIN", "https://portal.example.com")
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bundle := defaultProductionBundle(testLogger())
	wrap, err := configureIdentityHandler(&bundle, store)
	if err != nil {
		t.Fatal(err)
	}
	api, err := buildHandler(testLogger(), testBackend{}, 0, security.ModeProduction, bundle)
	if err != nil {
		t.Fatal(err)
	}
	handler := wrap(api)
	request := func(method, path, body string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", "https://portal.example.com")
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
	const login = "/api/v1/auth/login"
	initial := request("POST", login, `{"provider":"local","username":"admin","password":"admin123"}`, nil, 204).Result().Cookies()[0]
	request("GET", "/api/v1/clusters", "", initial, 403)
	request("GET", "/api/v1/admin/users", "", initial, 403)
	request("POST", "/api/v1/admin/password", `{"current":"admin123","password":"fixture-changed-password"}`, initial, 204)
	request("GET", "/api/v1/admin/status", "", initial, 401)
	request("POST", login, `{"provider":"local","username":"admin","password":"admin123"}`, nil, 401)
	admin := request("POST", login, `{"provider":"local","username":"admin","password":"fixture-changed-password"}`, nil, 204).Result().Cookies()[0]
	request("POST", "/api/v1/admin/users", `{"username":"alice","password":"fixture-alice-password","groups":["ops"]}`, admin, 204)
	alice := request("POST", login, `{"provider":"local","username":"alice","password":"fixture-alice-password"}`, nil, 204).Result().Cookies()[0]
	request("GET", "/api/v1/admin/users", "", alice, 403)
	request("GET", "/api/v1/clusters", "", alice, 403)
	request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[{"effect":"allow","groups":["local:ops"],"capabilities":["clusters:list"],"scopes":[{"cluster":"*"}]}]}`, admin, 204)
	request("GET", "/api/v1/clusters", "", alice, 200)
	t.Setenv("AUTH_LDAP_BIND_PASSWORD_FILE", "")
	const ldapPath = "/api/v1/admin/ldap"
	const settings = `{"url":"ldaps://directory.example.com","baseDN":"dc=example,dc=com","bindDN":"cn=reader,dc=example,dc=com","userFilter":"(uid=%s)","groupAttribute":"memberOf","bindPassword":"test-bind-password"}`
	request("PUT", ldapPath, settings, alice, 403)
	request("PUT", ldapPath, settings, admin, 204)
	request("PUT", ldapPath, strings.Replace(settings, "test-bind-password", "", 1), admin, 204)
	w := request("GET", ldapPath, "", admin, 200)
	if strings.Contains(w.Body.String(), "bindPassword") || !strings.Contains(w.Body.String(), `"passwordConfigured":true`) {
		t.Fatalf("LDAP credential response is not redacted: %s", w.Body.String())
	}
	reloaded, err := newIdentityAdmin(t.Context(), store)
	if err != nil || reloaded.ldap.BindPassword != "test-bind-password" {
		t.Fatal("LDAP configuration did not persist")
	}
	request("POST", "/api/v1/admin/ldap-test", `{}`, admin, 400)

}
