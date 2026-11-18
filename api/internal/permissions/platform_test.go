package permissions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
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

func TestGroupEditorSavesMetadataAndRoleAssociationsTogether(t *testing.T) {
	state := platformState{Groups: map[string]platformGroup{
		"ops": {ID: "ops", Name: "旧名称", Roles: []string{guestRole}},
	}}
	roles := []string{adminRole}
	result := applyAccess(state, platformChange{Action: "group-save", ID: "ops", Name: "新名称", RoleIDs: &roles})
	if result.Reason == "" || state.Groups["ops"].Name != "旧名称" {
		t.Fatalf("invalid role changed the group: %+v", state.Groups["ops"])
	}
	roles = []string{}
	result = applyAccess(state, platformChange{Action: "group-save", ID: "ops", Name: "新名称", Description: "用途", RoleIDs: &roles})
	group := state.Groups["ops"]
	if result.Reason != "" || group.Name != "新名称" || group.Description != "用途" || len(group.Roles) != 0 {
		t.Fatalf("group edit not saved together: %+v, %+v", result, group)
	}
}
