package identitystore

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/abchw517/kube-aiops/internal/identity"
)

func TestIdentityPersistenceAndAtomicImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	account := identity.LocalAccount{Username: "alice", Hash: "fixture-hash", Groups: []string{"ops"}}
	if err := store.Import(ctx, []identity.LocalAccount{account}, []byte("policy-1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Import(ctx, []identity.LocalAccount{account, account}, []byte("invalid")); err == nil {
		t.Fatal("expected duplicate import to fail")
	}
	if err := store.Import(ctx, nil, []byte("policy-2")); err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{Subject: "ldap:cn=bob,dc=example", Groups: []string{"ldap:cn=ops,dc=example"}}
	if err := store.RecordLDAPLogin(ctx, "bob", principal); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLDAPLogin(ctx, "bob@example", principal); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.LocalAccount(ctx, "alice")
	if err != nil || got.Hash != account.Hash || len(got.Groups) != 1 || got.Groups[0] != "ops" {
		t.Fatalf("account was not retained: %v", err)
	}
	policy, err := store.Policy(ctx)
	if err != nil || string(policy) != "policy-2" {
		t.Fatalf("policy was not retained: %v", err)
	}
	var username string
	if err := store.QueryRow("SELECT username FROM ldap_accounts WHERE subject=?", principal.Subject).Scan(&username); err != nil || username != "bob@example" {
		t.Fatalf("LDAP login was not updated: %v", err)
	}
	if err := store.Import(ctx, []identity.LocalAccount{}, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LocalAccount(ctx, "alice"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("account was not removed: %v", err)
	}
}
