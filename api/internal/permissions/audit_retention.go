package permissions

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const secondsPerDay = 86400
const auditExpired = `EXISTS (SELECT 1 FROM operation_audit_retention AS retention
	WHERE retention.risk=operation_audit.risk AND operation_audit.created_at <= ? - retention.days * 86400)`

type auditRetentionDays struct {
	Low  int64 `json:"low"`
	Med  int64 `json:"medium"`
	High int64 `json:"high"`
}

func (a *Admin) auditRetention(ctx context.Context) (auditRetentionDays, error) {
	var days auditRetentionDays
	err := a.store.QueryRowContext(ctx, `SELECT
		(SELECT days FROM operation_audit_retention WHERE risk='低'),
		(SELECT days FROM operation_audit_retention WHERE risk='中'),
		(SELECT days FROM operation_audit_retention WHERE risk='高')`).Scan(&days.Low, &days.Med, &days.High)
	return days, err
}

func pruneOperations(ctx context.Context, db auditExecutor, now time.Time) error {
	_, err := db.ExecContext(ctx, `DELETE FROM operation_audit WHERE
		(risk='低' AND created_at <= ? - (SELECT days FROM operation_audit_retention WHERE risk='低') * 86400) OR
		(risk='中' AND created_at <= ? - (SELECT days FROM operation_audit_retention WHERE risk='中') * 86400) OR
		(risk='高' AND created_at <= ? - (SELECT days FROM operation_audit_retention WHERE risk='高') * 86400)`,
		now.Unix(), now.Unix(), now.Unix())
	return err
}

func (a *Admin) RunAuditRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := pruneOperations(ctx, a.store, time.Now()); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "audit retention cleanup failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Admin) updateAuditRetention(w http.ResponseWriter, r *http.Request, actor identity.Principal) {
	if actor.Subject != superSubject || actor.Provider != AdminProvider {
		http.Error(w, "仅超级管理员可修改审计保留策略", http.StatusForbidden)
		return
	}
	var days auditRetentionDays
	if !adminhttp.Decode(w, r, &days) {
		http.Error(w, "请输入三个风险等级的保留天数", http.StatusBadRequest)
		return
	}
	for _, value := range []int64{days.Low, days.Med, days.High} {
		if value < 1 || value > math.MaxInt64/secondsPerDay {
			http.Error(w, "保留天数必须为有效的正整数", http.StatusBadRequest)
			return
		}
	}
	ctx := r.Context()
	if err := a.saveAuditRetention(ctx, days, actor.Subject); err != nil {
		slog.WarnContext(ctx, "cannot update audit retention", "error", err)
		http.Error(w, "无法保存审计保留策略", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) saveAuditRetention(ctx context.Context, days auditRetentionDays, subject string) error {
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	if err := pruneOperations(ctx, tx, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation_audit_retention SET days=CASE risk
		WHEN '低' THEN ? WHEN '中' THEN ? WHEN '高' THEN ? END`, days.Low, days.Med, days.High); err != nil {
		return err
	}
	if err := pruneOperations(ctx, tx, now); err != nil {
		return err
	}
	if err := writeOperation(ctx, tx, operation{
		Actor: subject, Action: "修改审计保留策略", Risk: "高",
		Target: fmt.Sprintf("低 %d 天 / 中 %d 天 / 高 %d 天", days.Low, days.Med, days.High),
	}); err != nil {
		return err
	}
	return tx.Commit()
}
