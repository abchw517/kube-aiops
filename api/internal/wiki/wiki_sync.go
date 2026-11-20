package wiki

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type wikiRemotePage struct {
	ID    string `json:"id"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type wikiRemoteDirectory struct {
	Pages []wikiRemotePage `json:"pages"`
	Next  string           `json:"next,omitempty"`
}

type wikiSyncResult struct {
	Saved   int    `json:"saved"`
	Skipped int    `json:"skipped"`
	Failed  int    `json:"failed"`
	Missing int    `json:"missing"`
	RunID   string `json:"runId,omitempty"`
}

var (
	wikiSyncBusy         = errors.New("sync already running")
	errSyncAuthorization = errors.New("wiki sync authorization changed")
	errSyncCredentials   = errors.New("wiki sync credentials unavailable")
)

func (a *Service) wikiSyncSource(ctx context.Context, p identity.Principal, id string) (wikiSource, error) {
	var source wikiSource
	err := a.store.QueryRowContext(ctx, "SELECT url,cluster,namespace FROM wiki_sources WHERE id=? AND NOT EXISTS(SELECT 1 FROM wiki_disabled_sources WHERE id=wiki_sources.id)", id).Scan(&source.URL, &source.Cluster, &source.Namespace)
	if err != nil {
		return source, err
	}
	decision, err := a.Authorize(ctx, authorization.DecisionRequest{Principal: p, Capability: authorization.CapabilityWikiSync, Scope: authorization.NamespaceScope(source.Cluster, source.Namespace)})
	if err != nil || !decision.Allowed {
		return source, errors.New("source permission denied")
	}
	return source, nil
}

func (a *Service) handleWikiSync(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	source, err := a.wikiSyncSource(r.Context(), p, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.executeWikiSync(w, r, p, id, source, "manual", "", "")
}

func (a *Service) executeWikiSync(w http.ResponseWriter, r *http.Request, p identity.Principal, id string, source wikiSource, trigger, parent, page string) {
	result, err := a.syncWikiDirectory(r.Context(), p, id, source, trigger, parent, page)
	if err != nil {
		if errors.Is(err, wikiSyncBusy) {
			http.Error(w, "此来源正在同步", http.StatusConflict)
		} else if errors.Is(err, errSyncAuthorization) {
			http.NotFound(w, r)
		} else {
			http.Error(w, safeWikiSyncError(err), http.StatusBadGateway)
		}
		return
	}
	adminhttp.WriteJSON(w, result)
}

func (a *Service) checkSyncAuthorization(ctx context.Context, p identity.Principal, id string, source wikiSource) error {
	current, err := a.wikiSyncSource(ctx, p, id)
	if err != nil || current != source || !currentSession(ctx) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errSyncAuthorization
	}
	return nil
}

func (a *Service) syncWikiDirectory(ctx context.Context, p identity.Principal, id string, source wikiSource, trigger, parent, targetPage string) (result wikiSyncResult, runErr error) {
	if err := a.checkSyncAuthorization(ctx, p, id, source); err != nil {
		return result, err
	}
	run, err := a.startWikiRun(ctx, id, trigger, parent, targetPage)
	if err != nil {
		return result, err
	}
	result.RunID = run
	defer func() {
		if err := a.finishWikiRun(ctx, run, result, runErr); runErr == nil && err != nil {
			runErr = err
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	credential, err := a.loadWikiCredential(ctx, id, p)
	if err != nil {
		return result, errSyncCredentials
	}
	base, err := url.Parse(source.URL)
	if err != nil {
		return result, err
	}
	cursor := base
	seen := make(map[string]bool)
	for range 20 {
		batch, err := a.readWikiDirectory(ctx, cursor, credential)
		if err != nil {
			return result, err
		}
		if err := a.checkSyncAuthorization(ctx, p, id, source); err != nil {
			return result, err
		}
		latest, err := a.loadWikiCredential(ctx, id, p)
		if err != nil || latest != credential {
			return result, errSyncCredentials
		}
		for _, page := range batch.Pages {
			if targetPage != "" && page.ID != targetPage {
				continue
			}
			if seen[page.ID] {
				continue
			}
			seen[page.ID] = true
			if err := a.checkSyncAuthorization(ctx, p, id, source); err != nil {
				return result, err
			}
			changed, saveErr := a.saveWikiRemotePage(ctx, id, source, page, run)
			switch {
			case errors.Is(saveErr, errSyncAuthorization):
				return result, saveErr
			case saveErr != nil:
				result.Failed++
				item := wikiRunItem{Status: "failed", Reason: "文档格式无效或无法保存候选"}
				if validRemotePageID(page.ID) {
					item.Page = page.ID
				}
				if err := saveWikiRunItem(ctx, a.store, run, item); err != nil {
					return result, err
				}
			case changed:
				result.Saved++
			default:
				result.Skipped++
			}
			if targetPage != "" {
				return result, nil
			}
		}
		if batch.Next == "" {
			if err := a.checkSyncAuthorization(ctx, p, id, source); err != nil {
				return result, err
			}
			if targetPage != "" {
				result.Failed++
				return result, saveWikiRunItem(ctx, a.store, run, wikiRunItem{Page: targetPage, Status: "failed", Reason: "远端目录中未找到指定页"})
			}
			missing, err := a.markMissingWikiPages(ctx, id, run, seen)
			if err != nil {
				return result, err
			}
			result.Missing = missing
			return result, nil
		}
		next, err := cursor.Parse(batch.Next)
		if err != nil || next.Scheme != base.Scheme || next.Host != base.Host || next.Path != base.Path || next.RawPath != base.RawPath || next.User != nil || next.Fragment != "" || next.String() == cursor.String() {
			return result, errors.New("unsafe remote Wiki pagination link")
		}
		cursor = next
	}
	return result, errors.New("remote Wiki pagination exceeded its budget")
}

func (a *Service) readWikiDirectory(ctx context.Context, target *url.URL, credential wikiCredential) (wikiRemoteDirectory, error) {
	var batch wikiRemoteDirectory
	client := &http.Client{Transport: a.wikiTransport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return batch, err
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(credential.Username, credential.Password)
	response, err := client.Do(req)
	if err != nil {
		return batch, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return batch, errors.New("remote Wiki directory unavailable")
	}
	reader := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := reader.Decode(&batch); err != nil || batch.Pages == nil || len(batch.Pages) > 100 {
		return batch, errors.New("invalid remote Wiki directory response")
	}
	return batch, nil
}

func (a *Service) handleWikiCheck(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	source, err := a.wikiSyncSource(r.Context(), p, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var credential wikiCredential
	if !adminhttp.Decode(w, r, &credential) {
		http.Error(w, "输入有效的 Wiki 凭据", http.StatusBadRequest)
		return
	}
	if credential.Username == "" && credential.Password == "" {
		credential, err = a.loadWikiCredential(r.Context(), id, p)
	}
	target, parseErr := url.Parse(source.URL)
	if err != nil || parseErr != nil || credential.Username == "" || credential.Password == "" {
		http.Error(w, "需要远端 Wiki 账号和密码", http.StatusBadRequest)
		return
	}
	batch, err := a.readWikiDirectory(r.Context(), target, credential)
	if err != nil {
		http.Error(w, "无法登录 Wiki 或读取指定目录", http.StatusBadGateway)
		return
	}
	adminhttp.WriteJSON(w, map[string]any{"firstPageCount": len(batch.Pages), "hasNext": batch.Next != ""})
}

func (a *Service) markMissingWikiPages(ctx context.Context, id, run string, seen map[string]bool) (int, error) {
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT NOT EXISTS(SELECT 1 FROM wiki_disabled_sources WHERE id=?)", id).Scan(&active); err != nil {
		return 0, err
	}
	if !active {
		return 0, errSyncAuthorization
	}
	rows, err := tx.QueryContext(ctx, "SELECT page_id FROM wiki_remote_pages WHERE source_id=? AND missing=0", id)
	if err != nil {
		return 0, err
	}
	var missing []string
	for rows.Next() {
		var page string
		if err := rows.Scan(&page); err != nil {
			rows.Close()
			return 0, err
		}
		if !seen[page] {
			missing = append(missing, page)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, page := range missing {
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_remote_pages SET missing=1 WHERE source_id=? AND page_id=?", id, page); err != nil {
			return 0, err
		}
		if err := saveWikiRunItem(ctx, tx, run, wikiRunItem{Page: page, Status: "missing", Reason: "完整枚举后未找到该页"}); err != nil {
			return 0, err
		}
	}
	return len(missing), tx.Commit()
}

func validRemotePageID(id string) bool {
	return len(id) > 0 && len(id) <= 256 && !strings.ContainsRune(id, 0)
}

func (a *Service) saveWikiRemotePage(ctx context.Context, sourceID string, source wikiSource, page wikiRemotePage, run string) (bool, error) {
	if !validRemotePageID(page.ID) {
		return false, errors.New("invalid remote page ID")
	}
	doc, err := NormalizeDocument(Document{Cluster: source.Cluster, Namespace: source.Namespace, Environment: "远端 Wiki", Business: sourceID, DocumentType: "操作规程", Title: page.Title, Body: page.Body})
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256([]byte(sourceID + "\x00" + page.ID))
	doc.ID = hex.EncodeToString(digest[:])
	sourceURL := source.URL
	if base, err := url.Parse(source.URL); err == nil && page.URL != "" {
		if target, err := base.Parse(page.URL); err == nil && target.Scheme == base.Scheme && target.Host == base.Host && target.User == nil {
			sourceURL = target.String()
		}
	}
	doc.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	docHash := sha256.Sum256([]byte(doc.Title + "\x00" + doc.Body))
	contentHash := hex.EncodeToString(docHash[:])

	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var disabled bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM wiki_disabled_sources WHERE id=?)", sourceID).Scan(&disabled); err != nil {
		return false, err
	}
	if disabled {
		return false, errSyncAuthorization
	}
	previous, err := LoadDocument(ctx, tx, doc.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var previousHash string
	if hashErr := tx.QueryRowContext(ctx, "SELECT content_hash FROM wiki_remote_pages WHERE source_id=? AND page_id=?", sourceID, page.ID).Scan(&previousHash); hashErr != nil && !errors.Is(hashErr, sql.ErrNoRows) {
		return false, hashErr
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO wiki_remote_pages(source_id,page_id,knowledge_id,source_url,content_hash,missing) VALUES(?,?,?,?,?,0)
		ON CONFLICT(source_id,page_id) DO UPDATE SET source_url=excluded.source_url,content_hash=excluded.content_hash,missing=0`, sourceID, page.ID, doc.ID, sourceURL, contentHash); err != nil {
		return false, err
	}
	if previousHash == contentHash || err == nil && previous.Title == doc.Title && previous.Body == doc.Body {
		if err := saveWikiRunItem(ctx, tx, run, wikiRunItem{Page: page.ID, Status: "skipped"}); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	doc.Metadata.SourceID = sourceID
	doc.Metadata.Source = "远端 Wiki · " + sourceID
	doc.Metadata.Targets = []Target{{Cluster: source.Cluster, Namespace: source.Namespace}}
	candidateKey := sha256.Sum256([]byte(doc.ID + "\x00" + contentHash + "\x00" + strconv.Itoa(previous.Revision)))
	candidate := Candidate{ID: hex.EncodeToString(candidateKey[:]), KnowledgeID: doc.ID, Owner: "remote:" + sourceID,
		Status: draftStatus, Version: 1, BaseRevision: previous.Revision, Document: doc}
	if err := insertCandidate(ctx, tx, candidate); err != nil {
		return false, err
	}
	if err := saveWikiRunItem(ctx, tx, run, wikiRunItem{Page: page.ID, Status: "saved"}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (a *Service) handleWikiSchedule(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
	if _, err := a.wikiSyncSource(r.Context(), p, id); err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		var minutes int
		var next int64
		var cron, zone, last string
		_ = a.store.QueryRowContext(ctx, "SELECT minutes,cron,time_zone,next_at,last_result FROM wiki_sync_schedules WHERE source_id=? AND provider=? AND subject=?", id, p.Provider, p.Subject).Scan(&minutes, &cron, &zone, &next, &last)
		adminhttp.WriteJSON(w, map[string]any{"intervalMinutes": minutes, "cron": cron, "timeZone": zone, "nextRun": next, "lastResult": last})
	case http.MethodPut:
		if p.Provider != permissions.AdminProvider && p.Provider != "local" {
			http.Error(w, "定时同步仅支持可在后台重新校验的本地账号", http.StatusForbidden)
			return
		}
		var input struct {
			Minutes int    `json:"intervalMinutes"`
			Cron    string `json:"cron"`
			Zone    string `json:"timeZone"`
		}
		if !adminhttp.Decode(w, r, &input) || input.Minutes < 0 || input.Minutes > 10080 {
			http.Error(w, "请输入 0 至 10080 分钟的间隔", http.StatusBadRequest)
			return
		}
		var err error
		next := time.Now().Add(time.Duration(input.Minutes) * time.Minute)
		if input.Cron != "" {
			schedule, err := parseWikiCron(input.Cron, input.Zone)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			next = schedule.next(time.Now())
			if next.IsZero() {
				http.Error(w, "Cron 未产生下次运行时间", http.StatusBadRequest)
				return
			}
		}
		if input.Minutes == 0 && input.Cron == "" {
			_, err = a.store.ExecContext(ctx, "DELETE FROM wiki_sync_schedules WHERE source_id=? AND provider=? AND subject=?", id, p.Provider, p.Subject)
		} else {
			if _, err := a.loadWikiCredential(ctx, id, p); err != nil {
				http.Error(w, "请先绑定远端 Wiki 凭据", http.StatusBadRequest)
				return
			}
			result, saveErr := a.store.ExecContext(ctx, `INSERT INTO wiki_sync_schedules VALUES(?,?,?,?,?,?,?,'')
				ON CONFLICT(source_id) DO UPDATE SET minutes=excluded.minutes,cron=excluded.cron,time_zone=excluded.time_zone,next_at=excluded.next_at
				WHERE provider=excluded.provider AND subject=excluded.subject`, id, p.Provider, p.Subject, input.Minutes, input.Cron, input.Zone, next.Unix())
			err = saveErr
			if err == nil {
				n, _ := result.RowsAffected()
				if n == 0 {
					http.Error(w, "该来源已有其他用户的计划", http.StatusConflict)
					return
				}
			}
		}
		if err != nil {
			http.Error(w, "无法保存同步计划", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *Service) RunSchedule(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runDueWikiSchedule(ctx)
		}
	}
}

func (a *Service) runDueWikiSchedule(ctx context.Context) {
	var id string
	var p identity.Principal
	var minutes int
	var due int64
	var cron, zone string
	err := a.store.QueryRowContext(ctx, "SELECT source_id,provider,subject,minutes,cron,time_zone,next_at FROM wiki_sync_schedules WHERE next_at>0 AND next_at<=? ORDER BY next_at LIMIT 1", time.Now().Unix()).Scan(&id, &p.Provider, &p.Subject, &minutes, &cron, &zone, &due)
	if err != nil {
		return
	}
	status := "同步失败"
	next := time.Now().Add(time.Duration(minutes) * time.Minute).Unix()
	if cron != "" {
		if schedule, err := parseWikiCron(cron, zone); err == nil {
			next = schedule.next(time.Now()).Unix()
		} else {
			next, status = 0, "同步计划无效"
		}
	}
	if p.Provider == "local" {
		account, err := a.store.LocalAccount(ctx, strings.TrimPrefix(p.Subject, "local:"))
		if err == nil {
			for _, group := range account.Groups {
				p.Groups = append(p.Groups, "local:"+group)
			}
		} else {
			next, status = 0, "配置用户已失效"
		}
	} else if _, initial, err := a.store.Admin(ctx); err != nil || initial {
		next, status = 0, "配置用户已失效"
	}
	if next > 0 {
		source, err := a.wikiSyncSource(ctx, p, id)
		if err != nil {
			next, status = 0, "配置用户权限已失效"
		} else if result, err := a.syncWikiDirectory(ctx, p, id, source, "scheduled", "", ""); err == nil {
			data, _ := json.Marshal(result)
			status = string(data)
		}
	}
	_, _ = a.store.ExecContext(ctx, "UPDATE wiki_sync_schedules SET next_at=?,last_result=? WHERE source_id=? AND next_at=? AND provider=? AND subject=?", next, status, id, due, p.Provider, p.Subject)
}
