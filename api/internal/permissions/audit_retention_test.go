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
	call(http.MethodPut, `{"low":-1,"medium":6,"high":7}`, super, 400)
	call(http.MethodPut, `{"medium":6,"high":7}`, super, 400)
	call(http.MethodPut, `{"low":null,"medium":6,"high":7}`, super, 400)
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

// TestZeroAuditRetention 验证停用风险级别后清理旧记录、禁止新写入且查询不可见。
func TestZeroAuditRetention(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &Admin{store: store}
	actor := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	put := func(body string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), "operations", actor)
		if w.Code != status {
			t.Fatalf("retention PUT got %d want %d: %s", w.Code, status, w.Body)
		}
	}
	a.RecordOperation(ctx, superSubject, "obsolete-low", "低", "fixture")
	a.RecordOperation(ctx, superSubject, "retained-medium", "中", "fixture")
	if _, err := store.ExecContext(ctx, `INSERT INTO operation_audit(created_at,actor,action,risk,target,result,reason,batch)
		VALUES (?,'local:admin','future-high','高','fixture','成功','','')`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	put(`{"low":0,"medium":5,"high":0}`, http.StatusNoContent)
	a.RecordOperation(ctx, superSubject, "disabled-high", "高", "fixture")
	a.RecordLogin(ctx, "employee", "ldap:employee", 204)
	var count, login int64
	if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM operation_audit").Scan(&count); err != nil || count != 1 {
		t.Fatalf("disabled records were stored: %d, %v", count, err)
	}
	if err := store.QueryRowContext(ctx, "SELECT created_at FROM account_last_login WHERE actor='ldap:employee'").Scan(&login); err != nil || login == 0 {
		t.Fatalf("account last login not maintained: %d, %v", login, err)
	}
	// 查询也必须屏蔽停用风险级别的记录，即使有旧版本或导入数据绕过写入入口。
	if _, err := store.ExecContext(ctx, `INSERT INTO operation_audit(created_at,actor,action,risk,target,result,reason,batch)
		VALUES (?,'local:admin','imported-low','低','fixture','成功','','')`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/?risk=低"} {
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest(http.MethodGet, path, nil), "operations", actor)
		if w.Code != 200 || strings.Contains(w.Body.String(), "imported-low") {
			t.Fatalf("disabled risk leaked through query: %d %s", w.Code, w.Body)
		}
	}
	put(`{"low":3,"medium":5,"high":7}`, http.StatusNoContent)
	a.RecordOperation(ctx, superSubject, "new-low", "低", "fixture")
	var low int64
	if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM operation_audit WHERE risk='低' AND action='new-low'").Scan(&low); err != nil || low != 1 {
		t.Fatalf("re-enabled retention did not resume recording: %d, %v", low, err)
	}
}
