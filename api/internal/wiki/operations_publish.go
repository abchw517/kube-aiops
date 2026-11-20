package wiki

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

func (a *Service) publishCandidate(w http.ResponseWriter, r *http.Request, p identity.Principal, c Candidate) {
	ctx := r.Context()
	if c.Status != reviewStatus {
		http.Error(w, "只有待审核候选可以发布", 409)
		return
	}
	doc, err := a.readyDocument(ctx, c.Document)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if !slices.EqualFunc(doc.Metadata.Targets, c.Document.Metadata.Targets, targetsEqual) {
		http.Error(w, "归属已变更，请驳回后重新送审", 409)
		return
	}
	old, err := LoadDocument(ctx, a.store.DB, c.KnowledgeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "无法检查生效版本", 503)
		return
	}
	if c.BaseRevision != old.Revision {
		http.Error(w, "生效版本已更新，请重新编辑", 409)
		return
	}
	targets := documentTargets(doc)
	if old.Revision > 0 && !a.allTargetsAllowed(ctx, p, authorization.CapabilityWikiPublish, documentTargets(old)) {
		http.Error(w, "无权修改原有可见范围", 403)
		return
	}
	isProduction := func(t Target) bool { return t.Environment == "production" }
	production := slices.ContainsFunc(targets, isProduction) ||
		old.Revision > 0 && slices.ContainsFunc(documentTargets(old), isProduction)
	expanded := old.Revision > 0 && slices.ContainsFunc(targets, func(t Target) bool {
		return !slices.ContainsFunc(documentTargets(old), func(o Target) bool { return o.Cluster == t.Cluster && o.Namespace == t.Namespace })
	}) || old.Revision == 0 && len(targets) > 1
	if (production || expanded) && (!permissions.IsAdministrator(p) || c.Owner == Owner(p)) {
		http.Error(w, "生产发布或扩大范围须由另一位有权管理员审核", 403)
		return
	}
	now := time.Now().UTC()
	doc.ID, doc.Revision, doc.CreatedAt = c.KnowledgeID, old.Revision+1, now.Format(time.RFC3339Nano)
	doc.Metadata.ReviewedAt = now.Format(time.DateOnly)
	err = a.commitPublication(ctx, p, c, old, doc, production || expanded)
	if errors.Is(err, errGovernanceConflict) {
		http.Error(w, "候选或生效版本已变更", 409)
		return
	}
	if err != nil {
		http.Error(w, "无法发布知识，请重试", 503)
		return
	}
	c.Status, c.Version, c.Document = publishedStatus, c.Version+1, doc
	adminhttp.WriteJSON(w, c)
}

func targetsEqual(left, right Target) bool {
	return left.Cluster == right.Cluster && left.Namespace == right.Namespace && left.Environment == right.Environment && slices.Equal(left.Businesses, right.Businesses)
}

var errGovernanceConflict = errors.New("wiki state changed")

func (a *Service) commitPublication(ctx context.Context, p identity.Principal, c Candidate, old, doc Document, high bool) error {
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := LoadDocument(ctx, tx, c.KnowledgeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current.Revision != old.Revision || current.Withdrawn != old.Withdrawn {
		return errGovernanceConflict
	}
	if c.Document.Metadata.SourceID != "" {
		var disabled bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM wiki_disabled_sources WHERE id=?)", c.Document.Metadata.SourceID).Scan(&disabled); err != nil {
			return err
		}
		if disabled {
			return errGovernanceConflict
		}
	}
	data, _ := json.Marshal(doc)
	res, err := tx.ExecContext(ctx, "UPDATE wiki_candidates SET status=?,version=version+1,document=?,updated_at=? WHERE id=? AND status=? AND version=?",
		publishedStatus, string(data), time.Now().Unix(), c.ID, reviewStatus, c.Version)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return errGovernanceConflict
	}
	versions := []Document{doc}
	if old.Revision > 0 {
		versions = append(versions, old)
	}
	for _, version := range versions {
		encoded, _ := json.Marshal(version)
		if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_revisions VALUES(?,?,?) ON CONFLICT DO NOTHING", doc.ID, version.Revision, string(encoded)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO wiki_documents VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
		cluster=excluded.cluster,namespace=excluded.namespace,environment=excluded.environment,business=excluded.business,
		document_type=excluded.document_type,title=excluded.title,body=excluded.body,created_at=excluded.created_at`,
		doc.ID, doc.Cluster, doc.Namespace, doc.Environment, doc.Business, doc.DocumentType, doc.Title, doc.Body, doc.CreatedAt); err != nil {
		return err
	}
	metadata, _ := json.Marshal(doc.Metadata)
	if _, err := tx.ExecContext(ctx, `INSERT INTO wiki_publications VALUES(?,0,?) ON CONFLICT(knowledge_id)
		DO UPDATE SET metadata=excluded.metadata,withdrawn=0`, doc.ID, string(metadata)); err != nil {
		return err
	}
	if err := writeWikiAudit(ctx, tx, p, "发布知识", doc, high); err != nil {
		return err
	}
	return tx.Commit()
}

func writeWikiAudit(ctx context.Context, tx *sql.Tx, p identity.Principal, action string, doc Document, high bool) error {
	risk := "中"
	if high {
		risk = "高"
	}
	targets, _ := json.Marshal(documentTargets(doc))
	_, err := tx.ExecContext(ctx, `INSERT INTO operation_audit(created_at,actor,action,risk,target,result,reason,batch)
		SELECT ?,?,?,?,?,'成功',?,'' WHERE NOT EXISTS(SELECT 1 FROM operation_audit_retention WHERE risk=? AND days=0)`,
		time.Now().Unix(), p.Subject, action, risk, doc.ID, fmt.Sprintf("v%d · %s", doc.Revision, string(targets)), risk)
	return err
}

func (a *Service) governDocument(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()
	doc, err := LoadDocument(ctx, a.store.DB, r.PathValue("id"))
	if err != nil || !a.allTargetsAllowed(ctx, p, authorization.CapabilityWikiPublish, documentTargets(doc)) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		adminhttp.WriteJSON(w, doc)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input struct {
		Action   string `json:"action"`
		Revision int    `json:"revision"`
		ReviewBy string `json:"reviewBy"`
		Team     string `json:"team"`
	}
	if !adminhttp.Decode(w, r, &input) {
		http.Error(w, "无效请求", 400)
		return
	}
	if input.Revision != doc.Revision || doc.Withdrawn {
		http.Error(w, "知识版本或状态已变更", 409)
		return
	}
	action := "撤回知识"
	withdrawn := input.Action == "withdraw"
	if !withdrawn {
		if input.Action != "review" || !validDate(input.ReviewBy) || doc.Metadata.ReviewedAt == "" {
			http.Error(w, "需要有效复核日期，历史条目请先提交修订", 400)
			return
		}
		action = "复核知识"
		doc.Metadata.ReviewBy = input.ReviewBy
		doc.Metadata.ReviewedAt = time.Now().UTC().Format(time.DateOnly)
		if input.Team != "" {
			doc.Metadata.Team = sanitizer.WikiText(input.Team, 256)
		}
	}
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "无法更新状态", 503)
		return
	}
	defer tx.Rollback()
	current, err := LoadDocument(ctx, tx, doc.ID)
	if err != nil || current.Revision != doc.Revision || current.Withdrawn {
		http.Error(w, "知识版本或状态已变更", 409)
		return
	}
	metadata, _ := json.Marshal(doc.Metadata)
	if _, err = tx.ExecContext(ctx, `INSERT INTO wiki_publications VALUES(?,?,?) ON CONFLICT(knowledge_id)
		DO UPDATE SET metadata=excluded.metadata,withdrawn=excluded.withdrawn`, doc.ID, withdrawn, string(metadata)); err != nil ||
		writeWikiAudit(ctx, tx, p, action, doc, withdrawn) != nil || tx.Commit() != nil {
		http.Error(w, "无法更新状态", 503)
		return
	}
	doc.Withdrawn = withdrawn
	adminhttp.WriteJSON(w, doc)
}

func (a *Service) governanceList(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	rows, err := a.store.QueryContext(r.Context(), publishedQuery+" ORDER BY d.created_at DESC,d.id")
	if err != nil {
		http.Error(w, "无法读取知识治理", 503)
		return
	}
	var all []Document
	for rows.Next() {
		var doc Document
		if err := readPublished(rows, &doc); err != nil {
			rows.Close()
			http.Error(w, "无法读取知识治理", 503)
			return
		}
		all = append(all, doc)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "无法读取知识治理", 503)
		return
	}
	out := []Document{}
	for _, doc := range all {
		if !a.allTargetsAllowed(r.Context(), p, authorization.CapabilityWikiPublish, documentTargets(doc)) {
			continue
		}
		doc.ScopePending, _ = a.unresolvedLegacyScope(r.Context(), doc)
		doc.Body = ""
		doc.Metadata.Sections = nil
		out = append(out, doc)
	}
	adminhttp.WriteJSON(w, out)
}
