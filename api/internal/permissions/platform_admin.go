package permissions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"golang.org/x/crypto/bcrypt"
)

func MustChangePassword(p identity.Principal) bool {
	return p.Provider == InitialAdminProvider || p.Provider == pendingProvider
}

// IsAdministrator 识别已认证的本地或 LDAP 系统管理员。
func IsAdministrator(p identity.Principal) bool {
	return p.Provider == AdminProvider ||
		p.Provider == employeeProvider && p.SessionVersion != "" && p.Administrator
}

func CanChangePassword(p identity.Principal) bool {
	return MustChangePassword(p) || p.Provider == AdminProvider || p.Provider == guestProvider
}

type operationResponse struct {
	http.ResponseWriter
	status int
}

func (w *operationResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *operationResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (a *Admin) AuditChange(w http.ResponseWriter, r *http.Request, actor, action, risk, target string, next http.HandlerFunc) {
	if r.Method == http.MethodGet || r.URL.Query().Get("test") == "true" || r.URL.Query().Get("action") == "recheck" {
		next(w, r)
		return
	}
	response := &operationResponse{ResponseWriter: w}
	next(response, r)
	if response.status >= 200 && response.status < 300 {
		a.RecordOperation(context.WithoutCancel(r.Context()), actor, action, risk, target)
	}
}

// ResetSuperPassword reads the replacement from standard input.
func ResetSuperPassword() error {
	path := os.Getenv("AUTH_SQLITE_PATH")
	if path == "" {
		return errors.New("AUTH_SQLITE_PATH is required")
	}
	if _, err := os.Stat(path); err != nil {
		return errors.New("identity database is unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 75))
	if err != nil {
		return err
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if !validNewPassword(password) || strings.ContainsAny(password, "\r\n") || password == initialAdminPassword {
		return errors.New("a temporary password of 12–72 bytes is required on stdin")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	store, err := identitystore.Open(path)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()
	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE managed_admin SET password_hash=?,initial=1 WHERE id=1", string(hash))
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("super administrator: %w", sql.ErrNoRows)
	}
	if err := writeOperation(ctx, tx, operation{Actor: "服务器", Action: "重置超级管理员密码", Risk: "高", Target: superSubject}); err != nil {
		return err
	}
	return tx.Commit()
}
