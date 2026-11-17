package permissions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestLDAPActivationRequiresVerificationOfExactDraft(t *testing.T) {
	t.Setenv("AUTH_LDAP_CA_FILE", "")
	t.Setenv("AUTH_LDAP_BIND_PASSWORD_FILE", "")
	writeIdentityTestFile(t, "AUTH_LDAP_ENCRYPTION_KEY_FILE", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	draft := `"url":"ldaps://directory.example.com","baseDN":"dc=example,dc=com","bindDN":"cn=reader,dc=example,dc=com","userFilter":"(uid=%s)","bindPassword":"fixture-bind"`
	a := &Admin{store: store, settings: ldapSettings{URL: "ldaps://old.example.com"}}
	probeStage := "success"
	a.ldapProbe = func(_ context.Context, cfg identity.LDAPConfig, user, password string) string {
		if cfg.BindPassword != "fixture-bind" || user != "employee" || password != "fixture-personal-password" {
			t.Fatal("probe did not receive the candidate configuration and employee credentials")
		}
		return probeStage
	}
	request := func(method, subject, fields string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/", strings.NewReader("{"+fields+"}"))
		ctx := identity.WithPrincipal(r.Context(), identity.Principal{Subject: subject, Provider: AdminProvider})
		a.handleLDAPChange(w, r.WithContext(ctx), method == http.MethodPost)
		if w.Code != status {
			t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body)
		}
		return w
	}
	const operator = "local:admin"
	const employee = `,"testUsername":"employee","testPassword":"fixture-personal-password"`
	request(http.MethodPut, operator, draft, http.StatusConflict)
	for _, failure := range []string{"connection", "bind", "user_match", "user_auth"} {
		probeStage = failure
		response := request(http.MethodPost, operator, draft+employee, http.StatusOK)
		if !strings.Contains(response.Body.String(), failure) || strings.Contains(response.Body.String(), "verification") {
			t.Fatalf("unexpected LDAP failure response: %s", response.Body)
		}
		if a.settings.URL != "ldaps://old.example.com" {
			t.Fatal("failed draft changed live LDAP configuration")
		}
	}
	probeStage = "success"
	response := request(http.MethodPost, operator, draft+employee, http.StatusOK)
	var result struct{ Stage, Verification string }
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Stage != "success" || result.Verification == "" || len(response.Result().Cookies()) > 0 {
		t.Fatalf("invalid probe result: %s", response.Body)
	}
	for _, query := range []string{"SELECT COUNT(*) FROM ldap_settings", "SELECT COUNT(*) FROM ldap_accounts", "SELECT COUNT(*) FROM authorization_roles"} {
		var count int
		if err := store.QueryRowContext(t.Context(), query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("probe wrote state: %d %v", count, err)
		}
	}
	ticket := `,"verification":"` + result.Verification + `"`
	request(http.MethodPut, "local:other", draft+ticket, http.StatusConflict)
	request(http.MethodPut, operator, strings.Replace(draft, "directory.example.com", "changed.example.com", 1)+ticket, http.StatusConflict)
	expires := a.ldapVerification.expires
	a.ldapVerification.expires = time.Now().Add(-time.Second)
	request(http.MethodPut, operator, draft+ticket, http.StatusConflict)
	a.ldapVerification.expires = expires
	if _, err := store.Exec("CREATE TRIGGER reject_ldap BEFORE INSERT ON ldap_settings BEGIN SELECT RAISE(ABORT, 'write failure'); END"); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPut, operator, draft+ticket, http.StatusServiceUnavailable)
	if a.settings.URL != "ldaps://old.example.com" {
		t.Fatal("failed save replaced live config")
	}
	if _, err := store.Exec("DROP TRIGGER reject_ldap"); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPut, operator, draft+ticket, http.StatusNoContent)
	request(http.MethodPut, operator, draft+ticket, http.StatusConflict)
	var document string
	if err := store.QueryRowContext(t.Context(), "SELECT document FROM ldap_settings").Scan(&document); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "fixture-bind") || strings.Contains(document, "fixture-personal-password") || !strings.Contains(document, "passwordCiphertext") {
		t.Fatalf("unexpected persisted credential fields: %s", document)
	}
	if a.settings.URL != "ldaps://directory.example.com" {
		t.Fatal("verified draft did not become active")
	}
}
