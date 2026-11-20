package wiki

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type wikiRun struct {
	ID       string `json:"id"`
	Trigger  string `json:"trigger"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	RetryOf  string `json:"retryOf,omitempty"`
	Page     string `json:"pageId,omitempty"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished,omitempty"`
	wikiSyncResult
}

type wikiRunItem struct {
	Page   string `json:"pageId,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (a *Service) startWikiRun(ctx context.Context, source, trigger, retryOf, page string) (string, error) {
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE wiki_sync_runs SET status='interrupted',reason='执行中断，可重新同步',finished_at=?
		WHERE source_id=? AND status='running' AND lease_until<=?`, now.Unix(), source, now.Unix()); err != nil {
		return "", err
	}
	id := rand.Text()
	inserted, err := tx.ExecContext(ctx, `INSERT INTO wiki_sync_runs(id,source_id,trigger,status,retry_of,page_id,started_at,lease_until)
		VALUES(?,?,?,'running',?,?,?,?) ON CONFLICT DO NOTHING`, id, source, trigger, retryOf, page, now.Unix(), now.Add(45*time.Second).Unix())
	if err != nil {
		return "", err
	}
	if count, err := inserted.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return "", err
		}
		return "", wikiSyncBusy
	}
	return id, tx.Commit()
}

func (a *Service) finishWikiRun(ctx context.Context, id string, result wikiSyncResult, runErr error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	state, reason := "succeeded", ""
	if runErr != nil {
		state, reason = "failed", safeWikiSyncError(runErr)
		if errors.Is(runErr, context.Canceled) {
			state = "interrupted"
		}
	} else if result.Failed > 0 {
		state = "partial"
	}
	_, err := a.store.ExecContext(ctx, `UPDATE wiki_sync_runs SET status=?,reason=?,finished_at=?
		WHERE id=? AND status='running'`, state, reason, time.Now().Unix(), id)
	return err
}

func safeWikiSyncError(err error) string {
	switch {
	case errors.Is(err, errSyncAuthorization):
		return "来源、范围或会话授权已失效"
	case errors.Is(err, errSyncCredentials):
		return "来源凭据缺失或已失效"
	case errors.Is(err, context.Canceled):
		return "执行中断，可重新同步"
	case errors.Is(err, context.DeadlineExceeded):
		return "同步超时，可重新同步"
	default:
		return "远端连接、目录格式或持久化失败"
	}
}

func saveWikiRunItem(ctx context.Context, db wikiWriter, id string, item wikiRunItem) error {
	_, err := db.ExecContext(ctx, "INSERT INTO wiki_sync_run_items(run_id,page_id,status,reason) VALUES(?,?,?,?)",
		id, item.Page, item.Status, item.Reason)
	return err
}

const wikiRunQuery = `SELECT r.id,r.trigger,
	CASE WHEN r.status='running' AND r.lease_until<=? THEN 'interrupted' ELSE r.status END,
	CASE WHEN r.status='running' AND r.lease_until<=? THEN '执行中断，可重新同步' ELSE r.reason END,
	r.retry_of,r.page_id,r.started_at,r.finished_at,
	(SELECT count(*) FROM wiki_sync_run_items WHERE run_id=r.id AND status='saved'),
	(SELECT count(*) FROM wiki_sync_run_items WHERE run_id=r.id AND status='skipped'),
	(SELECT count(*) FROM wiki_sync_run_items WHERE run_id=r.id AND status='failed'),
	(SELECT count(*) FROM wiki_sync_run_items WHERE run_id=r.id AND status='missing')
	FROM wiki_sync_runs r WHERE r.source_id=?`

func readWikiRun(row scanner) (wikiRun, error) {
	var item wikiRun
	err := row.Scan(&item.ID, &item.Trigger, &item.Status, &item.Reason, &item.RetryOf, &item.Page, &item.Started, &item.Finished, &item.Saved, &item.Skipped, &item.Failed, &item.Missing)
	return item, err
}

func (a *Service) syncRuns(w http.ResponseWriter, r *http.Request, p identity.Principal, sourceID string) {
	ctx := r.Context()
	source, err := a.wikiSyncSource(ctx, p, sourceID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	now := time.Now().Unix()
	id := r.PathValue("run")
	if id == "" && r.Method == http.MethodGet {
		rows, err := a.store.QueryContext(ctx, wikiRunQuery+" ORDER BY r.started_at DESC,r.rowid DESC LIMIT 30", now, now, sourceID)
		if err != nil {
			http.Error(w, "无法读取同步记录", 503)
			return
		}
		defer rows.Close()
		items := []wikiRun{}
		for rows.Next() {
			item, err := readWikiRun(rows)
			if err != nil {
				http.Error(w, "无法读取同步记录", 503)
				return
			}
			items = append(items, item)
		}
		if rows.Err() != nil {
			http.Error(w, "无法读取同步记录", 503)
			return
		}
		rows.Close()
		var lastSucceeded int64
		if err := a.store.QueryRowContext(ctx, "SELECT COALESCE(MAX(finished_at),0) FROM wiki_sync_runs WHERE source_id=? AND status='succeeded' AND page_id=''", sourceID).Scan(&lastSucceeded); err != nil {
			http.Error(w, "无法读取最近同步时间", 503)
			return
		}
		adminhttp.WriteJSON(w, map[string]any{"items": items, "lastSucceeded": lastSucceeded})
		return
	}
	if id == "" {
		w.WriteHeader(405)
		return
	}
	run, err := readWikiRun(a.store.QueryRowContext(ctx, wikiRunQuery+" AND r.id=?", now, now, sourceID, id))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost && r.PathValue("action") == "retry" {
		a.retryWikiRun(w, r, p, sourceID, source, run)
		return
	}
	if r.Method != http.MethodGet || r.PathValue("action") != "" {
		w.WriteHeader(405)
		return
	}
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if r.URL.Query().Get("page") == "" {
		page, err = 1, nil
	}
	if err != nil || page < 1 || page > 100 {
		http.Error(w, "页码无效", 400)
		return
	}
	query := "SELECT page_id,status,reason FROM wiki_sync_run_items WHERE run_id=?"
	if r.URL.Query().Get("failed") == "true" {
		query += " AND status='failed'"
	}
	rows, err := a.store.QueryContext(ctx, query+" ORDER BY id LIMIT 51 OFFSET ?", id, (page-1)*50)
	if err != nil {
		http.Error(w, "无法读取同步明细", 503)
		return
	}
	defer rows.Close()
	items := []wikiRunItem{}
	for rows.Next() {
		var item wikiRunItem
		if err := rows.Scan(&item.Page, &item.Status, &item.Reason); err != nil {
			http.Error(w, "无法读取同步明细", 503)
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		http.Error(w, "无法读取同步明细", 503)
		return
	}
	adminhttp.WriteJSON(w, map[string]any{"run": run, "items": items[:min(50, len(items))], "more": len(items) > 50})
}

func (a *Service) retryWikiRun(w http.ResponseWriter, r *http.Request, p identity.Principal, sourceID string, source wikiSource, run wikiRun) {
	if run.Status != "partial" && run.Status != "failed" && run.Status != "interrupted" {
		http.Error(w, "此任务无需重试", 409)
		return
	}
	var input struct {
		Page string `json:"pageId"`
	}
	if !adminhttp.Decode(w, r, &input) || input.Page != "" && !validRemotePageID(input.Page) {
		http.Error(w, "请选择有效的失败页", 400)
		return
	}
	if input.Page != "" {
		var valid bool
		err := a.store.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM wiki_sync_run_items WHERE run_id=? AND page_id=? AND status='failed')", run.ID, input.Page).Scan(&valid)
		if err != nil || !valid {
			http.NotFound(w, r)
			return
		}
	}
	a.executeWikiSync(w, r, p, sourceID, source, "retry", run.ID, input.Page)
}
