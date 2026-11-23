package permissions

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestInterfaceGrantsDoNotCombineActionsAndScopes(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	a.SetScopeResolvers(func(_ context.Context, id string) (string, error) {
		return id, nil
	}, func(_ context.Context, cluster, namespace, business string) (bool, error) {
		return namespace == "pay" && business == "retail", nil
	})
	p, err := a.registerEmployee(ctx, "fixture", identity.Principal{Subject: "ldap:uid=fixture,dc=example"})
	if err != nil {
		t.Fatal(err)
	}
	writeRole := platformRole{ID: "write", Name: "测试复核", Grants: []interfaceGrant{{Code: catalog.ReviewSave,
		Scope: accessScope{Environment: "testing", Cluster: "testing", Namespace: "pay"}}}}
	readRole := platformRole{ID: "read", Name: "生产查看", Grants: []interfaceGrant{{Code: catalog.ReviewRead,
		Scope: accessScope{Environment: "production", Cluster: "production", Namespace: "pay", Business: "retail"}}}}
	change := func(f func(*platformState)) {
		t.Helper()
		if err := a.editPlatform(ctx, func(s *platformState, _ *sql.Tx) error { f(s); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	change(func(s *platformState) {
		s.Roles[writeRole.ID], s.Roles[readRole.ID] = writeRole, readRole
		s.Groups["ops"] = platformGroup{ID: "ops", Name: "值班组", Roles: []string{"write", "read"}}
		account := s.Accounts[p.Subject]
		account.Roles, account.Groups = []string{"write", guestRole}, []string{"ops"}
		s.Accounts[p.Subject] = account
	})
	check := func(code, cluster, ns string, expected bool) {
		t.Helper()
		allowed, err := a.AuthorizeOperation(ctx, p, code, "", authorization.NamespaceScope(cluster, ns), false)
		if err != nil || allowed != expected {
			t.Fatalf("%s on %s/%s: got %v, %v; want %v", code, cluster, ns, allowed, err, expected)
		}
	}
	check(catalog.ReviewSave, "testing", "pay", true)
	check(catalog.ReviewSave, "production", "pay", false)
	check(catalog.ReviewRead, "production", "pay", true)
	check(catalog.ReviewRead, "production", "other", false)
	check(catalog.ReviewRead, "production", "", false)
	check(catalog.FindingRead, "testing", "another", true)
	check("system.retention.update", "*", "", false)
	state, err := a.platform(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, grant := range state.effectiveGrants(state.Accounts[p.Subject]) {
		if grant.Code == catalog.ReviewSave {
			found = slices.Equal(grant.Sources, []string{"直接授予 → 测试复核", "用户组 值班组 → 测试复核"}) ||
				slices.Equal(grant.Sources, []string{"用户组 值班组 → 测试复核", "直接授予 → 测试复核"})
		}
	}
	if !found {
		t.Fatal("missing both grant sources")
	}
	change(func(s *platformState) {
		account := s.Accounts[p.Subject]
		account.Roles = []string{guestRole}
		s.Accounts[p.Subject] = account
	})
	check(catalog.ReviewSave, "testing", "pay", true)
	change(func(s *platformState) {
		group := s.Groups["ops"]
		group.Roles = []string{"read"}
		s.Groups["ops"] = group
	})
	check(catalog.ReviewSave, "testing", "pay", false)
}

func TestInterfaceGrantSaveValidationAndOptimisticConflict(t *testing.T) {
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	a.SetScopeResolvers(func(_ context.Context, cluster string) (string, error) { return cluster, nil }, nil)
	actor := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	call := func(body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), "access", actor)
		if w.Code != status {
			t.Fatalf("status=%d, want %d: %s", w.Code, status, w.Body)
		}
		return w
	}
	call(`{"action":"role-save","name":"invalid","grants":[{"code":"unknown","scope":{}}]}`, 400)
	call(`{"action":"role-save","name":"invalid","grants":[{"code":"system.retention.update","scope":{}}]}`, 400)
	call(`{"action":"role-save","name":"invalid","grants":[{"code":"system.audit.list","scope":{"environment":"production"}}]}`, 400)
	first := call(`{"action":"role-save","name":"审计观察员","grants":[{"code":"system.audit.list","scope":{}}]}`, 200)
	if first.Header().Get("X-Operation-Audit-Ref") == "" {
		t.Fatal("role save did not return audit reference")
	}
	var result []changeResult
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil || len(result) != 1 {
		t.Fatalf("unexpected save response: %v %v", result, err)
	}
	state, err := a.platform(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id := result[0].ID
	prior := state.Roles[id]
	employee, err := a.registerEmployee(t.Context(), "fixture", identity.Principal{Subject: "ldap:uid=fixture,dc=example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.editPlatform(t.Context(), func(state *platformState, _ *sql.Tx) error {
		account := state.Accounts[employee.Subject]
		account.Roles = append(account.Roles, id)
		state.Accounts[employee.Subject] = account
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check := func(code string, scope authorization.Scope, expected bool) {
		t.Helper()
		allowed, err := a.AuthorizeOperation(t.Context(), employee, code, "", scope, false)
		if err != nil || allowed != expected {
			t.Fatalf("authorize %s in %+v: %v, %v; want %v", code, scope, allowed, err, expected)
		}
	}
	testScope := authorization.NamespaceScope("testing", "pay")
	check(catalog.ReviewSave, testScope, false)
	check("system.audit.list", authorization.GlobalScope(), true)
	update := platformChange{ID: id, Action: "role-save", Name: prior.Name, PreviousRole: &prior,
		Grants: &[]interfaceGrant{{Code: "system.audit.record"}, {Code: catalog.ReviewSave,
			Scope: accessScope{Environment: "testing", Cluster: "testing", Namespace: "pay"}}}}
	body, _ := json.Marshal(update)
	call(string(body), 200)
	call(string(body), 409)
	state, err = a.platform(t.Context())
	if err != nil || len(state.Roles[id].Grants) != 2 {
		t.Fatalf("incorrectly applied grant update: %v %v", state.Roles[id], err)
	}
	check(catalog.ReviewSave, testScope, true)
	check(catalog.ReviewSave, authorization.NamespaceScope("production", "pay"), false)
	check("system.audit.record", authorization.GlobalScope(), true)
	check("system.audit.list", authorization.GlobalScope(), false)
}
