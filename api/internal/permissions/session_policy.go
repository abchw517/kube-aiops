package permissions

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const readSessionLifetime = "SELECT minutes FROM login_session_policy WHERE id=1"

type sessionPolicy struct {
	Minutes int `json:"sessionLifetimeMinutes"`
}

// SessionLifetimeMinutes 读取已提交的会话策略，供登录签发使用。
func (a *Admin) SessionLifetimeMinutes(ctx context.Context) (int, error) {
	var minutes int
	err := a.store.QueryRowContext(ctx, readSessionLifetime).Scan(&minutes)
	return minutes, err
}

// handleSessionPolicy 处理管理员查看和超级管理员保存会话有效期的请求。
func (a *Admin) handleSessionPolicy(w http.ResponseWriter, r *http.Request, actor identity.Principal) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		minutes, err := a.SessionLifetimeMinutes(ctx)
		if err != nil {
			slog.WarnContext(ctx, "cannot read session policy", "error", err)
			http.Error(w, "无法读取登录有效期", http.StatusServiceUnavailable)
			return
		}
		adminhttp.WriteJSON(w, sessionPolicy{Minutes: minutes})
	case http.MethodPut:
		if actor.Subject != superSubject || actor.Provider != AdminProvider {
			http.Error(w, "仅本地超级管理员可修改登录有效期", http.StatusForbidden)
			return
		}
		var input sessionPolicy
		if !adminhttp.Decode(w, r, &input) || !identity.ValidSessionLifetimeMinutes(input.Minutes) {
			http.Error(w, "请输入 5 至 480 之间的整数。", http.StatusBadRequest)
			return
		}
		if err := a.saveSessionPolicy(ctx, actor.Subject, input.Minutes); err != nil {
			slog.WarnContext(ctx, "cannot save session policy", "error", err)
			http.Error(w, "无法保存登录有效期", http.StatusServiceUnavailable)
			return
		}
		adminhttp.WriteJSON(w, input)
	default:
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// saveSessionPolicy 在同一事务内写入新策略与审计，避免记录未提交的变更。
func (a *Admin) saveSessionPolicy(ctx context.Context, subject string, minutes int) error {
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var previous int
	if err := tx.QueryRowContext(ctx, readSessionLifetime).Scan(&previous); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE login_session_policy SET minutes=? WHERE id=1", minutes); err != nil {
		return err
	}
	if err := writeOperation(ctx, tx, operation{
		Actor: subject, Action: "修改登录有效期", Risk: "高",
		Target: fmt.Sprintf("%d 分钟 → %d 分钟", previous, minutes),
	}); err != nil {
		return err
	}
	return tx.Commit()
}
