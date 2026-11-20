package permissions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestAdministratorGroupPreviewAndSessionChanges(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	super := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	const employee = "ldap:employee"
	const backup = "ldap:backup"
	const ordinary = "ldap:ordinary"
	const addedLater = "ldap:added-later"
	const disabled = "ldap:disabled"
	if err := a.editPlatform(ctx, func(state *platformState, _ *sql.Tx) error {
		state.Groups["ops"] = platformGroup{ID: "ops", Name: "值守"}
		state.Groups["backup"] = platformGroup{ID: "backup", Name: "应急", Roles: []string{adminRole}}
		state.Accounts[employee] = platformAccount{ID: employee, Username: "employee", Kind: "ldap", Generation: "old", Groups: []string{"ops"}}
		state.Accounts[backup] = platformAccount{ID: backup, Username: "backup", Kind: "ldap", Generation: "keep", Groups: []string{"ops", "backup"}, Roles: []string{adminRole}}
		state.Accounts[ordinary] = platformAccount{ID: ordinary, Username: "ordinary", Kind: "ldap", Generation: "admin", Groups: []string{"backup"}}
		state.Accounts[addedLater] = platformAccount{ID: addedLater, Username: "added-later", Kind: "ldap", Generation: "later"}
		state.Accounts[disabled] = platformAccount{ID: disabled, Username: "disabled", Kind: "ldap", Generation: "disabled", Groups: []string{"ops"}, Disabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	login := func(id string) identity.Principal {
		t.Helper()
		principal, err := a.registerEmployee(ctx, strings.TrimPrefix(id, "ldap:"), identity.Principal{Subject: id})
		if err != nil {
			t.Fatal(err)
		}
		return principal
	}
	call := func(actor identity.Principal, resource, method string, change platformChange, want int) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(change)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest(method, "/", strings.NewReader(string(body))), resource, actor)
		if w.Code != want {
			t.Fatalf("%s %s %s: got %d, want %d: %s", resource, change.Action, change.ID, w.Code, want, w.Body)
		}
		return w
	}
	preview := func(change platformChange) groupAdministratorPreview {
		t.Helper()
		change.Action = "group-preview"
		w := call(super, "access", http.MethodPost, change, 200)
		var result groupAdministratorPreview
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Version == "" {
			t.Fatalf("invalid preview: %+v (%v)", result, err)
		}
		return result
	}
	old, retained, normalAdmin := login(employee), login(backup), login(ordinary)
	if IsAdministrator(old) || !IsAdministrator(retained) || !IsAdministrator(normalAdmin) {
		t.Fatal("initial effective identities are incorrect")
	}
	roles := []string{adminRole}
	change := platformChange{Action: "group-save", ID: "ops", Name: "值守", RoleIDs: &roles}
	call(normalAdmin, "access", http.MethodPost, change, 409)
	call(normalAdmin, "access", http.MethodPost, platformChange{Action: "group-preview", ID: "ops", RoleIDs: &roles}, 403)
	call(super, "access", http.MethodPost, change, 409)
	first := preview(change)
	if !first.AddsAdministrator || len(first.Members) != 3 || first.Members[1].Eligible ||
		len(first.Members[0].Before) != 2 || len(first.Members[0].After) != 3 {
		t.Fatalf("unexpected impact: %+v", first)
	}
	call(normalAdmin, "accounts", http.MethodPost, platformChange{Action: "join", ID: addedLater, Values: []string{"ops"}}, 200)
	change.GroupPreview = first.Version
	call(super, "access", http.MethodPost, change, 409)
	if err := a.ValidateSession(ctx, old); err != nil {
		t.Fatalf("stale group preview changed a session: %v", err)
	}
	change.GroupPreview = preview(change).Version
	saved := call(super, "access", http.MethodPost, change, 200)
	ref := saved.Header().Get("X-Operation-Audit-Ref")
	if ref == "" {
		t.Fatal("group save did not return audit reference")
	}
	var risk, reason string
	if err := store.QueryRowContext(ctx, "SELECT risk, reason FROM operation_audit WHERE reference=?", ref).Scan(&risk, &reason); err != nil ||
		risk != "高" || !strings.Contains(reason, employee) || !strings.Contains(reason, "保留管理员来源 1 人") {
		t.Fatalf("incorrect audit: %s %s (%v)", risk, reason, err)
	}
	if err := a.ValidateSession(ctx, old); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("unchanged old employee session: %v", err)
	}
	if err := a.ValidateSession(ctx, retained); err != nil {
		t.Fatalf("unnecessarily expired a retained administrator: %v", err)
	}
	elevated := login(employee)
	if !IsAdministrator(elevated) || elevated.Provider != employeeProvider {
		t.Fatalf("group administrator did not retain LDAP provider: %+v", elevated)
	}

	users := call(normalAdmin, "accounts", http.MethodGet, platformChange{}, 200)
	var accounts []accountView
	if err := json.Unmarshal(users.Body.Bytes(), &accounts); err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(accounts, func(account accountView) bool { return account.ID == employee })
	if index < 0 || !slices.Equal(accounts[index].AdministratorSources, []string{"用户组 值守"}) {
		t.Fatalf("inherited administrator source not shown: %+v", accounts)
	}

	for _, action := range []string{"delete", "disable", "leave"} {
		call(normalAdmin, "accounts", http.MethodPost, platformChange{Action: action, ID: employee, Values: []string{"ops"}}, 409)
	}
	call(normalAdmin, "accounts", http.MethodPost, platformChange{Action: "join", ID: disabled, Values: []string{"ops"}}, 409)
	blocked := call(normalAdmin, "access", http.MethodPost, platformChange{Action: "group-roles-remove", ID: "ops", Values: roles}, 200)
	if !strings.Contains(blocked.Body.String(), "仅超级管理员") {
		t.Fatalf("batch operation missed authorization error: %s", blocked.Body)
	}

	change.RoleIDs = &[]string{}
	change.GroupPreview = preview(change).Version
	call(super, "access", http.MethodPost, change, 200)
	if err := a.ValidateSession(ctx, elevated); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("elevated session survived removal of its last source: %v", err)
	}
	if err := a.ValidateSession(ctx, retained); err != nil || IsAdministrator(login(employee)) {
		t.Fatalf("revocation did not preserve independent sources: %v", err)
	}

	change = platformChange{Action: "group-save", Name: "新建空组", RoleIDs: &roles}
	empty := preview(change)
	if len(empty.Members) != 0 {
		t.Fatalf("empty group had members: %+v", empty)
	}
	change.GroupPreview = empty.Version
	w := call(super, "access", http.MethodPost, change, 200)
	var groups []changeResult
	if err := json.Unmarshal(w.Body.Bytes(), &groups); err != nil || len(groups) != 1 || groups[0].ID == "" {
		t.Fatalf("new group was not saved: %v (%v)", groups, err)
	}
	groupID := groups[0].ID
	call(normalAdmin, "accounts", http.MethodPost, platformChange{Action: "join", ID: employee, Values: []string{groupID}}, 409)
	call(super, "accounts", http.MethodPost, platformChange{Action: "join", ID: employee, Values: []string{groupID}}, 200)
	if !IsAdministrator(login(employee)) {
		t.Fatal("employee joining a preconfigured group did not inherit administrator role")
	}
}

func TestAdministratorGroupRoleBatchUpdatesEffectiveIdentity(t *testing.T) {
	state := platformState{
		Groups:   map[string]platformGroup{"ops": {ID: "ops", Name: "值守"}},
		Accounts: map[string]platformAccount{"employee": {ID: "employee", Kind: "ldap", Groups: []string{"ops"}, Generation: "prior"}},
	}
	input := platformChange{Action: "group-roles-add", ID: "ops"}
	if result := applyGroupRole(state, input, adminRole, false); result.Reason == "" || state.Groups["ops"].hasAdministratorRole() {
		t.Fatal("ordinary administrator added a group administrator role")
	}
	if result := applyGroupRole(state, input, adminRole, true); result.Reason != "" {
		t.Fatalf("super administrator could not apply explicitly authorized batch operation: %+v", result)
	}
	account := state.Accounts["employee"]
	if account.Generation == "prior" || !state.isAdministrator(account) {
		t.Fatal("batch role change did not update identity and session")
	}
}
