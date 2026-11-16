package main

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/identitystore"
	"github.com/abchw517/kube-aiops/internal/security"
	"golang.org/x/crypto/bcrypt"
)

func TestManagedIdentityBootstrap(t *testing.T) {
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "true")
	t.Setenv("AUTH_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	t.Setenv("AUTH_LDAP_CA_FILE", "")
	writeIdentityTestFile(t, "AUTH_LDAP_ENCRYPTION_KEY_FILE", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("AUTH_ORIGIN", "https://portal.example.com")
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := newIdentityAdmin(t.Context(), store); err == nil {
		t.Fatal("expected bootstrap to require an explicit password file")
	}
	writeIdentityTestFile(t, "AUTH_BOOTSTRAP_ADMIN_PASSWORD_FILE", "fixture-initial-admin-password\n")
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
	initial := request("POST", login, `{"provider":"local","username":"admin","password":"fixture-initial-admin-password"}`, nil, 204).Result().Cookies()[0]
	request("GET", "/api/v1/clusters", "", initial, 403)
	request("GET", "/api/v1/admin/users", "", initial, 403)
	request("POST", "/api/v1/admin/password", `{"current":"fixture-initial-admin-password","password":"fixture-changed-password"}`, initial, 204)
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
	var document string
	if err := store.QueryRowContext(t.Context(), "SELECT document FROM ldap_settings").Scan(&document); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "test-bind-password") || !strings.Contains(document, "passwordCiphertext") {
		t.Fatal("LDAP search password was not encrypted in SQLite")
	}
	reloaded, err := newIdentityAdmin(t.Context(), store)
	if err != nil || reloaded.ldap == nil || reloaded.ldap.BindPassword != "test-bind-password" {
		t.Fatal("LDAP configuration did not persist")
	}
	request("POST", "/api/v1/admin/ldap-test", `{}`, admin, 400)

}

func TestLDAPConfigurationFailurePreservesLocalAdminAndMigratesPassword(t *testing.T) {
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "")
	t.Setenv("AUTH_LDAP_CA_FILE", "")
	t.Setenv("AUTH_LDAP_ENCRYPTION_KEY_FILE", "")
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-local-admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(t.Context(), "INSERT INTO managed_admin VALUES (1, ?, 0)", string(hash)); err != nil {
		t.Fatal(err)
	}
	const legacy = `{"url":"ldaps://directory.example.com","baseDN":"dc=example,dc=com","bindDN":"cn=reader,dc=example,dc=com","bindPassword":"fixture-legacy-password"}`
	if _, err := store.ExecContext(t.Context(), "INSERT INTO ldap_settings VALUES (1, ?)", legacy); err != nil {
		t.Fatal(err)
	}
	assertLocalRecovery := func() {
		t.Helper()
		admin, err := newIdentityAdmin(t.Context(), store)
		if err != nil || admin.ldap != nil || admin.ldapIssue == "" {
			t.Fatalf("expected LDAP-only failure, startup error=%v", err)
		}
		if p, err := admin.local(t.Context(), "admin", "fixture-local-admin"); err != nil || p.Provider != adminProvider {
			t.Fatalf("local recovery unavailable: %v", err)
		}
		if _, err := admin.directory(t.Context(), "alice", "fixture-user"); !errors.Is(err, identity.ErrAuthenticationUnavailable) {
			t.Fatalf("LDAP failure=%v", err)
		}
	}
	assertLocalRecovery()

	writeIdentityTestFile(t, "AUTH_LDAP_ENCRYPTION_KEY_FILE", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("AUTH_LDAP_CA_FILE", filepath.Join(t.TempDir(), "missing-ca.pem"))
	assertLocalRecovery()
	var document string
	if err := store.QueryRowContext(t.Context(), "SELECT document FROM ldap_settings").Scan(&document); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "fixture-legacy-password") || !strings.Contains(document, "passwordCiphertext") {
		t.Fatal("legacy plaintext document was not migrated")
	}

	t.Setenv("AUTH_LDAP_CA_FILE", "")
	admin, err := newIdentityAdmin(t.Context(), store)
	if err != nil || admin.ldap == nil || admin.ldap.BindPassword != "fixture-legacy-password" {
		t.Fatalf("LDAP setup did not recover after CA repair: %v", err)
	}
	replacementKey := make([]byte, 32)
	replacementKey[0] = 1
	writeIdentityTestFile(t, "AUTH_LDAP_ENCRYPTION_KEY_FILE", base64.StdEncoding.EncodeToString(replacementKey))
	assertLocalRecovery()
}

func TestLegacyInitialAdministratorRequiresReplacement(t *testing.T) {
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "")
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte(legacyInitialAdminPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapAdmin(t.Context(), string(hash)); err != nil {
		t.Fatal(err)
	}
	admin, err := newIdentityAdmin(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.local(t.Context(), "admin", legacyInitialAdminPassword); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("legacy initial password unexpectedly accepted: %v", err)
	}
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "true")
	writeIdentityTestFile(t, "AUTH_BOOTSTRAP_ADMIN_PASSWORD_FILE", "fixture-replacement-initial-password")
	admin, err = newIdentityAdmin(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := admin.local(t.Context(), "admin", "fixture-replacement-initial-password"); err != nil || p.Provider != initialAdminProvider {
		t.Fatalf("replacement bootstrap did not require first-login password change: %v", err)
	}
	t.Setenv("AUTH_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	if _, err := newIdentityAdmin(t.Context(), store); err != nil {
		t.Fatalf("restart should preserve the initialized password hash: %v", err)
	}
}

func writeIdentityTestFile(t *testing.T, variable, contents string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(variable, path)
}
