package permissions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestPlatformRoleSourcesAndImmediateAccessChanges(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	a.SetScopeResolvers(func(_ context.Context, id string) (string, error) {
		if id == "prod" {
			return "production", nil
		}
		return "development", nil
	}, func(_ context.Context, cluster, namespace, business string) (bool, error) {
		return business == "cloud", nil
	})
	p, err := a.registerEmployee(ctx, "alice", identity.Principal{Subject: "ldap:uid=alice,dc=example", Provider: "ldap", Groups: []string{"ldap:directory-group"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Groups) != 0 {
		t.Fatal("directory groups leaked into platform authorization")
	}

	a.RecordLogin(ctx, "alice", p.Subject, 204)
	a.RecordLogin(ctx, "alice", p.Subject, 401)
	accounts := httptest.NewRecorder()
	a.handlePlatform(accounts, httptest.NewRequest("GET", "/", nil), "accounts", identity.Principal{Subject: superSubject, Provider: AdminProvider})
	var users []accountView
	if err := json.Unmarshal(accounts.Body.Bytes(), &users); err != nil || accounts.Code != 200 ||
		len(users) != 1 || users[0].ID != p.Subject || users[0].LastLoginAt == 0 {
		t.Fatalf("successful login missing from accounts: %d %s %v", accounts.Code, accounts.Body, err)
	}

	change := func(resource, body string, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), resource, identity.Principal{Subject: superSubject, Provider: AdminProvider})
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body)
		}
		return w.Body.String()
	}

	if err := a.editPlatform(ctx, func(s *platformState, _ *sql.Tx) error {
		s.Roles["read-dev"] = platformRole{ID: "read-dev", Name: "研发诊断", Scopes: []accessScope{{Environment: "development"}}}
		s.Roles["read-prod"] = platformRole{ID: "read-prod", Name: "生产诊断", Scopes: []accessScope{{Environment: "production", Cluster: "prod", Namespace: "payments", Business: "cloud"}}}
		s.Groups["ops"] = platformGroup{ID: "ops", Name: "运维", Roles: []string{"read-dev", "read-prod"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	change("accounts", `{"action":"grant","id":"ldap:uid=alice,dc=example","values":["read-dev"]}`, 200)
	reply := change("accounts", `{"action":"join","ids":["local:admin","ldap:uid=alice,dc=example"],"values":["ops"]}`, 200)
	if !strings.Contains(reply, "账号不可修改") {
		t.Fatal(reply)
	}
	state, err := a.platform(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scopes := state.effective(state.Accounts[p.Subject])
	if len(scopes) != 3 || len(scopes[0].Sources) != 3 {
		t.Fatalf("sources: %+v", scopes)
	}
	allow := func(cluster, namespace string, want bool) {
		t.Helper()
		decision, err := a.Authorize(ctx, authorization.DecisionRequest{Principal: p, Capability: authorization.CapabilityFindingsRead,
			Scope: authorization.NamespaceScope(cluster, namespace)})
		if err != nil || decision.Allowed != want {
			t.Fatalf("%s/%s: %+v %v", cluster, namespace, decision, err)
		}
	}
	allow("prod", "payments", true)
	allow("prod", "", false)
	allow("prod", "other", false)
	a.namespaceBusiness = func(context.Context, string, string, string) (bool, error) { return false, nil }
	allow("prod", "payments", false)
	change("access", `{"action":"role-delete","id":"read-dev"}`, 409)
	change("access", `{"action":"role-delete","id":"guest-readonly"}`, 409)
	change("accounts", `{"action":"revoke","id":"ldap:uid=alice,dc=example","values":["guest-readonly","read-dev"]}`, 200)
	p, err = a.registerEmployee(ctx, "alice", identity.Principal{Subject: p.Subject})
	if err != nil {
		t.Fatal(err)
	}
	state, err = a.platform(ctx)
	if err != nil || len(state.Accounts[p.Subject].Roles) != 0 {
		t.Fatal("login must not restore the guest role when a group role remains")
	}
	change("accounts", `{"action":"leave","id":"ldap:uid=alice,dc=example","values":["ops"]}`, 200)
	allow("dev", "", false)
	p, err = a.registerEmployee(ctx, "alice", identity.Principal{Subject: p.Subject})
	if err != nil {
		t.Fatal(err)
	}
	allow("dev", "", true)
	change("accounts", `{"action":"disable","id":"ldap:uid=alice,dc=example"}`, 200)
	if err := a.ValidateSession(ctx, p); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("session still valid: %v", err)
	}
	if _, err := a.registerEmployee(ctx, "alice", identity.Principal{Subject: p.Subject}); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("disabled account logged in: %v", err)
	}
	var failures int
	if err := store.QueryRowContext(ctx, "SELECT count(*) FROM operation_audit WHERE result='失败' AND batch<>''").Scan(&failures); err != nil || failures != 1 {
		t.Fatalf("missing partial batch audit: %d, %v", failures, err)
	}
}

// TestBatchAdministratorRoleForLDAP 验证直接角色批量操作、管理员边界与会话失效。
func TestBatchAdministratorRoleForLDAP(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	login := func(subject string) identity.Principal {
		t.Helper()
		p, err := a.registerEmployee(ctx, subject, identity.Principal{Subject: subject})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	alice, bob := login("ldap:alice"), login("ldap:bob")
	if err := a.editPlatform(ctx, func(s *platformState, _ *sql.Tx) error {
		s.Roles["read-dev"] = platformRole{ID: "read-dev", Name: "研发诊断"}
		s.Roles["read-test"] = platformRole{ID: "read-test", Name: "测试诊断"}
		s.Accounts["local:operator"] = platformAccount{ID: "local:operator", Kind: "administrator", Generation: "fixture-operator"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	super := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	operator := identity.Principal{Subject: "local:operator", Provider: AdminProvider, SessionVersion: "fixture-operator"}
	change := func(actor identity.Principal, body string, want int) []changeResult {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), "accounts", actor)
		if w.Code != want {
			t.Fatalf("status %d want %d: %s", w.Code, want, w.Body)
		}
		if want != 200 {
			return nil
		}
		var results []changeResult
		if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
			t.Fatal(err)
		}
		return results
	}
	grant := `{"action":"grant","ids":["ldap:alice","ldap:bob"],"values":["read-dev","read-test","system-admin"]}`
	for _, result := range change(operator, grant, 200) {
		if !strings.Contains(result.Reason, "仅超级管理员") {
			t.Fatalf("ordinary administrator changed the protected role: %+v", result)
		}
	}
	state, err := a.platform(ctx)
	if err != nil || !slices.Equal(state.Accounts[alice.Subject].Roles, []string{guestRole}) {
		t.Fatalf("invalid batch partially applied roles: %v", err)
	}
	granted := change(super, grant, 200)
	if len(granted) != 2 {
		t.Fatalf("missing batch outcomes: %+v", granted)
	}
	for _, result := range granted {
		if result.Reason != "" {
			t.Fatalf("super administrator grant failed: %+v", result)
		}
	}
	for _, old := range []identity.Principal{alice, bob} {
		if err := a.ValidateSession(ctx, old); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("old LDAP session survived role grant: %v", err)
		}
	}
	elevated := login(alice.Subject)
	decision, err := a.Authorize(ctx, authorization.DecisionRequest{Principal: elevated, Capability: authorization.CapabilityEnvironmentsManage, Scope: authorization.GlobalScope()})
	if err != nil || !decision.Allowed || !IsAdministrator(elevated) || elevated.Provider != employeeProvider || CanChangePassword(elevated) {
		t.Fatalf("LDAP administration or directory identity incorrect: %+v, %v", elevated, err)
	}
	change(elevated, `{"action":"revoke","id":"ldap:bob","values":["system-admin"]}`, 409)
	change(operator, `{"action":"disable","id":"ldap:alice"}`, 409)
	for _, result := range change(super, `{"action":"revoke","ids":["ldap:alice","ldap:bob"],"values":["system-admin","read-dev"]}`, 200) {
		if result.Reason != "" {
			t.Fatalf("super administrator revoke failed: %+v", result)
		}
	}
	if err := a.ValidateSession(ctx, elevated); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("privileged LDAP session survived revocation: %v", err)
	}
	regular := login(alice.Subject)
	state, err = a.platform(ctx)
	if err != nil || IsAdministrator(regular) || !slices.Equal(state.Accounts[alice.Subject].Roles, []string{guestRole, "read-test"}) {
		t.Fatalf("revocation changed unrelated roles: %+v (%v)", regular, err)
	}
}

func TestGroupEditorSavesMetadataAndRoleAssociationsTogether(t *testing.T) {
	state := platformState{Groups: map[string]platformGroup{
		"ops": {ID: "ops", Name: "旧名称", Roles: []string{guestRole}},
	}}
	roles := []string{adminRole}
	result := applyAccess(state, platformChange{Action: "group-save", ID: "ops", Name: "新名称", RoleIDs: &roles}, false)
	if result.Reason == "" || state.Groups["ops"].Name != "旧名称" {
		t.Fatalf("invalid role changed the group: %+v", state.Groups["ops"])
	}
	roles = []string{}
	result = applyAccess(state, platformChange{Action: "group-save", ID: "ops", Name: "新名称", Description: "用途", RoleIDs: &roles}, false)
	group := state.Groups["ops"]
	if result.Reason != "" || group.Name != "新名称" || group.Description != "用途" || len(group.Roles) != 0 {
		t.Fatalf("group edit not saved together: %+v, %+v", result, group)
	}
}

func TestDeleteSelectedAccountsRevokesSessionsAndPreventsLDAPRegistration(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	directory := identity.Principal{Subject: "ldap:uid=employee,dc=example", Provider: "ldap"}
	employee, err := a.registerEmployee(ctx, "employee", directory)
	if err != nil {
		t.Fatal(err)
	}
	admin := platformAccount{ID: "local:operator", Kind: "administrator", Generation: "operator-session"}
	guest := platformAccount{ID: "local:visitor", Kind: "guest", Generation: "visitor-session", Roles: []string{guestRole}}
	if err := a.editPlatform(ctx, func(s *platformState, _ *sql.Tx) error {
		s.Accounts[admin.ID], s.Accounts[guest.ID] = admin, guest
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	remove := func(actor identity.Principal, body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), "accounts", actor)
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body)
		}
	}
	super := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	remove(super, `{"action":"delete","id":"local:admin"}`, 409)
	remove((platformState{}).principal(admin), `{"action":"delete","id":"local:operator"}`, 409)
	remove(super, `{"action":"delete","ids":["local:visitor"]}`, 400)
	remove((platformState{}).principal(admin), `{"action":"delete","id":"local:visitor"}`, 200)
	remove((platformState{}).principal(admin), `{"action":"delete","id":"ldap:uid=employee,dc=example"}`, 200)
	remove(super, `{"action":"delete","id":"local:operator"}`, 200)
	for _, p := range []identity.Principal{(platformState{}).principal(guest), employee, (platformState{}).principal(admin)} {
		if err := a.ValidateSession(ctx, p); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("deleted account %s retained a session: %v", p.Subject, err)
		}
	}
	if _, err := a.registerEmployee(ctx, "employee", directory); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("deleted LDAP account could register again: %v", err)
	}
	state, err := a.platform(ctx)
	if err != nil || len(state.Accounts) != 0 {
		t.Fatalf("deleted accounts still in platform state: %v %v", state.Accounts, err)
	}
	var count int
	if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM operation_audit WHERE action='删除账号' AND risk='高'").Scan(&count); err != nil || count != 3 {
		t.Fatalf("expected three high-risk deletion events, got %d, %v", count, err)
	}
}

func TestAssignmentAuditReferenceAndRetention(t *testing.T) {
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	ctx := t.Context()
	if _, err := a.registerEmployee(ctx, "alice", identity.Principal{Subject: "ldap:alice"}); err != nil {
		t.Fatal(err)
	}
	if err := a.editPlatform(ctx, func(s *platformState, _ *sql.Tx) error {
		s.Roles["reader"] = platformRole{ID: "reader", Name: "Reader"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	actor := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	request := func(method, resource, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest(method, path, strings.NewReader(body)), resource, actor)
		if w.Code != want {
			t.Fatalf("%s %s: %d, want %d: %s", method, resource, w.Code, want, w.Body)
		}
		return w
	}
	records := func(id string) []operation {
		t.Helper()
		w := request("GET", "operations", "/?record="+id+"&page=1&limit=10", "", 200)
		var payload struct{ Items []operation }
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Items
	}
	single := request("POST", "accounts", "/", `{"action":"grant","id":"ldap:alice","values":["reader"]}`, 200)
	id := single.Header().Get("X-Operation-Audit-Ref")
	if id == "" {
		t.Fatal("successful grant did not link a persisted audit event")
	}
	if items := records(id); len(items) != 1 || items[0].Target != "ldap:alice" || items[0].Batch != "" {
		t.Fatalf("incorrect single audit: %+v", items)
	}
	batch := request("POST", "accounts", "/", `{"action":"revoke","ids":["ldap:alice","local:admin"],"values":["reader"]}`, 200)
	batchID := batch.Header().Get("X-Operation-Audit-Ref")
	if batchID == "" || batchID == id {
		t.Fatal("batch did not link a distinct audit event")
	}
	if items := records(batchID); len(items) != 1 || items[0].Batch == "" || items[0].Result != "部分成功" {
		t.Fatalf("incorrect batch audit: %+v", items)
	}
	request("PUT", "operations", "/", `{"low":3,"medium":5,"high":0}`, 204)
	suppressed := request("POST", "accounts", "/", `{"action":"grant","id":"ldap:alice","values":["reader"]}`, 200)
	if suppressed.Header().Get("X-Operation-Audit-Ref") != "" || len(records(id)) != 0 {
		t.Fatal("audit shortcut revealed records excluded by retention")
	}
	request("PUT", "operations", "/", `{"low":3,"medium":5,"high":7}`, 204)
	request("POST", "accounts", "/", `{"action":"revoke","id":"ldap:alice","values":["reader"]}`, 200)
	if len(records(id)) != 0 || len(records(batchID)) != 0 {
		t.Fatal("a purged link resolved to a later event")
	}
	request("GET", "operations", "/?record=invalid", "", 400)
}

func TestRoleSnapshotPreventsOverwritingConcurrentChange(t *testing.T) {
	development := accessScope{Environment: "development"}
	testingScope := accessScope{Environment: "testing"}
	old := platformRole{ID: "reader", Name: "Reader", Scopes: []accessScope{development}}
	state := platformState{Roles: map[string]platformRole{"reader": old}}
	first := platformChange{Action: "role-save", ID: old.ID, Name: old.Name,
		PreviousRole: &old, Scopes: []accessScope{development, testingScope, testingScope}}
	if result := applyAccess(state, first, true); result.Reason != "" {
		t.Fatal(result.Reason)
	}
	if role := state.Roles[old.ID]; !slices.Equal(role.Scopes, []accessScope{development, testingScope}) {
		t.Fatalf("scopes were not deduplicated: %+v", role.Scopes)
	}
	stale := platformChange{Action: "role-save", ID: old.ID, Name: "Renamed",
		PreviousRole: &old, Scopes: []accessScope{development}}
	if result := applyAccess(state, stale, true); !strings.Contains(result.Reason, "其他操作修改") {
		t.Fatalf("stale snapshot was accepted: %+v", result)
	}
	if role := state.Roles[old.ID]; role.Name != old.Name || !slices.Contains(role.Scopes, testingScope) {
		t.Fatalf("stale write overwrote concurrent change: %+v", role)
	}
}
