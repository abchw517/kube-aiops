package permissions

import (
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

func TestAuditRetentionHidesAndPrunesExpiredRecords(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "identity.db")
	store, err := identitystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	a := &Admin{store: store}
	now := time.Now()
	for _, record := range []struct {
		risk   string
		age    int
		action string
	}{
		{"低", 3, "登录"}, {"中", 5, "expired-middle"}, {"高", 7, "expired-high"},
		{"低", 2, "keep-low"}, {"中", 4, "keep-middle"}, {"高", 6, "keep-high"},
	} {
		if _, err := store.ExecContext(ctx, `INSERT INTO operation_audit(created_at,actor,action,risk,target,result,reason,batch)
			VALUES (?,'local:admin',?,?,'admin','成功','','')`,
			now.Add(-time.Duration(record.age)*24*time.Hour).Unix(), record.action, record.risk); err != nil {
			t.Fatal(err)
		}
	}
	super := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	call := func(method, body string, actor identity.Principal, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest(method, "/", strings.NewReader(body)), "operations", actor)
		if w.Code != want {
			t.Fatalf("%s operations returned %d, want %d: %s", method, w.Code, want, w.Body.String())
		}
		return w.Body.String()
	}
	var page struct {
		Items     []operation
		Retention auditRetentionDays
	}
	if err := json.Unmarshal([]byte(call(http.MethodGet, "", super, 200)), &page); err != nil {
		t.Fatal(err)
	}
	if page.Retention != (auditRetentionDays{3, 5, 7}) || len(page.Items) != 3 {
		t.Fatalf("default policy or visible records incorrect: %+v", page)
	}
	for _, item := range page.Items {
		if !strings.HasPrefix(item.Action, "keep-") {
			t.Fatalf("expired record visible: %+v", item)
		}
	}
	const custom = `{"low":1,"medium":6,"high":7}`
	call(http.MethodPut, custom, identity.Principal{Subject: "local:other", Provider: AdminProvider}, 403)
	call(http.MethodPut, `{"low":0,"medium":6,"high":7}`, super, 400)
	call(http.MethodPut, custom, super, 204)
	// Persisted settings and the most recent successful login survive a restart.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = identitystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a.store = store
	actual, err := a.auditRetention(ctx)
	if err != nil || actual != (auditRetentionDays{1, 6, 7}) {
		t.Fatalf("saved policy = %+v, %v", actual, err)
	}
	var count, changes int
	if err := store.QueryRowContext(ctx, "SELECT COUNT(*), SUM(action='修改审计保留策略') FROM operation_audit").Scan(&count, &changes); err != nil || count != 3 || changes != 1 {
		t.Fatalf("expected two retained records and one audit change, got count=%d changes=%d err=%v", count, changes, err)
	}
	var login int64
	if err := store.QueryRowContext(ctx, "SELECT created_at FROM account_last_login WHERE actor=?", superSubject).Scan(&login); err != nil || login != now.Add(-72*time.Hour).Unix() {
		t.Fatalf("most recent login = %d, %v", login, err)
	}
}
