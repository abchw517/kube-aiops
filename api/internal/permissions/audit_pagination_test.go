package permissions

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

// TestAuditOperationsPagination 验证分页边界、筛选口径和旧游标协议。
func TestAuditOperationsPagination(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	insert := func(created time.Time, actor, action, risk, target, batch string) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_audit
			(created_at,actor,action,risk,target,result,reason,batch) VALUES (?,?,?,?,?,'成功','',?)`,
			created.Unix(), actor, action, risk, target, batch); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for index := 1; index <= 123; index++ {
		switch index % 6 {
		case 0:
			insert(now, "local:auditor", "edit", "中", "ldap:alice", "b1")
		case 1:
			insert(now, "ldap:alice", "create", "中", "catalog", "b1")
		case 2:
			insert(now, "ldap:alice", "edit", "高", "catalog", "b1")
		case 3:
			insert(now, "ldap:alice", "edit", "中", "catalog", "b1")
		case 4:
			insert(now, "ldap:alice", "edit", "中", "catalog", "b2")
		case 5:
			insert(now, "ldap:bob", "edit", "中", "catalog", "b1")
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE operation_audit_retention SET days=0 WHERE risk='低'"); err != nil {
		t.Fatal(err)
	}
	insert(now, "ldap:alice", "disabled", "低", "catalog", "b1")
	insert(now.Add(-6*24*time.Hour), "ldap:alice", "edit", "中", "catalog", "b1")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a := &Admin{store: store}
	admin := identity.Principal{Subject: superSubject, Provider: AdminProvider}
	type response struct {
		Items     []operation
		Before    int64
		Page      int64
		Limit     int64
		Total     int64
		Actions   []string
		Retention *auditRetentionDays
	}
	get := func(query string, status int) response {
		t.Helper()
		w := httptest.NewRecorder()
		a.handlePlatform(w, httptest.NewRequest(http.MethodGet, "/"+query, nil), "operations", admin)
		if w.Code != status {
			t.Fatalf("GET %s returned %d, want %d: %s", query, w.Code, status, w.Body)
		}
		var page response
		if status == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Items == nil {
				t.Fatal("审计记录为空时应返回数组")
			}
		}
		return page
	}

	for _, test := range []struct {
		query              string
		page, limit, total int64
		rows               int
		first, last        int64
	}{
		{"?page=1", 1, 10, 123, 10, 123, 114},
		{"?page=2", 2, 10, 123, 10, 113, 104},
		{"?limit=1", 1, 1, 123, 1, 123, 123},
		{"?page=2&limit=25", 2, 25, 123, 25, 98, 74},
		{"?page=2&limit=20", 2, 20, 123, 20, 103, 84},
		{"?limit=50", 1, 50, 123, 50, 123, 74},
		{"?page=2&limit=50", 2, 50, 123, 50, 73, 24},
		{"?page=1&limit=100", 1, 100, 123, 100, 123, 24},
		{"?page=3&limit=50", 3, 50, 123, 23, 23, 1},
		{"?page=9223372036854775807&limit=100", 2, 100, 123, 23, 23, 1},
		{"?page=2&limit=20&action=edit&risk=中&account=ALICE&batch=b1", 2, 20, 41, 20, 63, 6},
		{"?page=2&limit=50&risk=低", 1, 50, 0, 0, 0, 0},
		{"?page=2&limit=50&account=unknown", 1, 50, 0, 0, 0, 0},
	} {
		t.Run(test.query, func(t *testing.T) {
			// 单次查询内总数、本页行和排序采用同一筛选范围。
			page := get(test.query, http.StatusOK)
			if page.Page != test.page || page.Limit != test.limit || page.Total != test.total || len(page.Items) != test.rows {
				t.Fatalf("page metadata or row count wrong: %+v", page)
			}
			if test.rows > 0 && (page.Items[0].ID != test.first || page.Items[len(page.Items)-1].ID != test.last) {
				t.Fatalf("wrong row bounds for %s: got %d..%d", test.query, page.Items[0].ID, page.Items[len(page.Items)-1].ID)
			}
			for i := 1; i < len(page.Items); i++ {
				if page.Items[i-1].ID <= page.Items[i].ID {
					t.Fatal("rows are not sorted by descending ID")
				}
			}
		})
	}

	first := get("?page=1&limit=10", http.StatusOK)
	if first.Retention == nil || first.Retention.Low != 0 || len(first.Actions) != 2 {
		t.Fatalf("missing first-page metadata: retention=%+v actions=%v", first.Retention, first.Actions)
	}
	legacy := get("", http.StatusOK)
	if len(legacy.Items) != 100 || legacy.Before != 24 || legacy.Retention == nil {
		t.Fatalf("legacy cursor response changed: rows=%d before=%d retention=%v", len(legacy.Items), legacy.Before, legacy.Retention)
	}
	legacy = get("?before=24", http.StatusOK)
	if len(legacy.Items) != 23 || legacy.Items[0].ID != 23 || legacy.Before != 0 || legacy.Retention != nil {
		t.Fatalf("legacy cursor did not advance: rows=%d before=%d retention=%v", len(legacy.Items), legacy.Before, legacy.Retention)
	}
	for _, query := range []string{
		"?page=0", "?page=-2", "?page=z", "?page=", "?page=9223372036854775808",
		"?limit=0", "?limit=-1", "?limit=101", "?limit=10.5", "?limit=1e1", "?limit=", "?before=50&page=1",
	} {
		get(query, http.StatusBadRequest)
	}
}
