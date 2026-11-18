package permissions

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"golang.org/x/crypto/bcrypt"
)

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
		admin, err := New(t.Context(), store)
		if err != nil || admin.ldap != nil || admin.ldapIssue == "" {
			t.Fatalf("expected LDAP-only failure, startup error=%v", err)
		}
		if p, err := admin.Local(t.Context(), "admin", "fixture-local-admin"); err != nil || p.Provider != AdminProvider {
			t.Fatalf("local recovery unavailable: %v", err)
		}
		if _, err := admin.Directory(t.Context(), "alice", "fixture-user"); !errors.Is(err, identity.ErrAuthenticationUnavailable) {
			t.Fatalf("LDAP failure=%v", err)
		}
	}
	admin, err := New(t.Context(), store)
	if err != nil || admin.ldap == nil || admin.ldap.BindPassword != "fixture-legacy-password" {
		t.Fatalf("existing SQLite LDAP configuration should load without a key: %v", err)
	}

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
	admin, err = New(t.Context(), store)
	if err != nil || admin.ldap == nil || admin.ldap.BindPassword != "fixture-legacy-password" {
		t.Fatalf("LDAP setup did not recover after CA repair: %v", err)
	}
	replacementKey := make([]byte, 32)
	replacementKey[0] = 1
	writeIdentityTestFile(t, "AUTH_LDAP_ENCRYPTION_KEY_FILE", base64.StdEncoding.EncodeToString(replacementKey))
	assertLocalRecovery()
	t.Setenv("AUTH_LDAP_ENCRYPTION_KEY_FILE", "")
	assertLocalRecovery()
	var lockedDocument string
	if err := store.QueryRowContext(t.Context(), "SELECT document FROM ldap_settings").Scan(&lockedDocument); err != nil || lockedDocument != document {
		t.Fatal("an encrypted LDAP configuration must remain intact when its key is unavailable")
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
