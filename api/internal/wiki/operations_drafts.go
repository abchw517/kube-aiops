package wiki

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

const (
	draftStatus     = "draft"
	reviewStatus    = "review"
	publishedStatus = "published"
	rejectedStatus  = "rejected"
	discardedStatus = "discarded"
)

type Candidate struct {
	ID           string   `json:"id"`
	KnowledgeID  string   `json:"knowledgeId"`
	Owner        string   `json:"owner"`
	Status       string   `json:"status"`
	Version      int      `json:"version"`
	BaseRevision int      `json:"baseRevision"`
	Document     Document `json:"document"`
	UpdatedAt    int64    `json:"updatedAt"`
	Reason       string   `json:"reason,omitempty"`
}

type candidateAction struct {
	Action   string   `json:"action"`
	Version  int      `json:"version"`
	Document Document `json:"document"`
	Reason   string   `json:"reason"`
}

var requiredSections = map[string][]string{
	"排障手册": {"conditions", "signals", "checks", "recovery", "escalation"},
	"故障复盘": {"impact", "evidence", "conclusion", "recovery"},
	"操作规程": {"conditions", "steps", "rollback", "recovery"},
	"常见问题": {"question", "answer", "conditions"},
}

func draftRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	adminhttp.WriteJSON(w, map[string]string{"code": "WIKI_DRAFT_REQUIRED", "message": "请通过知识草稿提交审核"})
}

func Owner(p identity.Principal) string { return p.Subject }

func randomID() (string, error) {
	var raw [32]byte
	_, err := rand.Read(raw[:])
	return hex.EncodeToString(raw[:]), err
}

const candidateQuery = "SELECT id,knowledge_id,owner,status,version,base_revision,document,updated_at,reason FROM wiki_candidates"

func readCandidate(row scanner) (Candidate, error) {
	var c Candidate
	var data string
	err := row.Scan(&c.ID, &c.KnowledgeID, &c.Owner, &c.Status, &c.Version, &c.BaseRevision, &data, &c.UpdatedAt, &c.Reason)
	if err == nil {
		err = json.Unmarshal([]byte(data), &c.Document)
	}
	return c, err
}

type wikiWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertCandidate(ctx context.Context, db wikiWriter, c Candidate) error {
	data, err := json.Marshal(c.Document)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO wiki_candidates(id,knowledge_id,owner,status,version,base_revision,document,updated_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, c.ID, c.KnowledgeID, c.Owner, c.Status, c.Version, c.BaseRevision, string(data), time.Now().Unix())
	return err
}

func (a *Service) allTargetsAllowed(ctx context.Context, p identity.Principal, cap authorization.Capability, targets []Target) bool {
	if len(targets) == 0 {
		return false
	}
	for _, target := range targets {
		if yes, err := a.allowedTarget(ctx, p, cap, target); err != nil || !yes {
			return false
		}
	}
	return true
}

func draftDocument(doc Document) (Document, error) {
	if len(doc.Body) > 48<<10 || len(doc.Title) > 256 || len(doc.Metadata.Targets) > 16 {
		return doc, errors.New("标题、正文或范围超出限制")
	}
	if _, ok := requiredSections[doc.DocumentType]; !ok {
		return doc, errors.New("请选择有效知识类型")
	}
	doc.Links = nil
	doc.ScopePending = false
	doc.ID, doc.Revision, doc.CreatedAt, doc.Match, doc.Withdrawn, doc.CurrentRevision = "", 0, "", "", false, 0
	doc.Metadata.ReviewedAt = ""
	doc.Metadata.Team = sanitizer.WikiText(strings.TrimSpace(doc.Metadata.Team), 256)
	doc.Metadata.Applicability = sanitizer.WikiText(strings.TrimSpace(doc.Metadata.Applicability), 2048)
	doc.Metadata.ResourceType = sanitizer.WikiText(strings.TrimSpace(doc.Metadata.ResourceType), 120)
	doc.Metadata.Source = sanitizer.WikiText(strings.TrimSpace(doc.Metadata.Source), 512)
	doc.Title = sanitizer.WikiText(strings.TrimSpace(doc.Title), 256)
	doc.Body = sanitizer.WikiText(strings.TrimSpace(doc.Body), 48<<10)
	var signals []string
	for _, signal := range doc.Metadata.Signals {
		if value := sanitizer.WikiText(strings.TrimSpace(signal), 180); value != "" && !slices.Contains(signals, value) {
			signals = append(signals, value)
		}
		if len(signals) >= 20 {
			break
		}
	}
	doc.Metadata.Signals = signals
	valid := make(map[string]string)
	for _, key := range requiredSections[doc.DocumentType] {
		if value := strings.TrimSpace(doc.Metadata.Sections[key]); value != "" {
			valid[key] = sanitizer.WikiText(value, 6200)
		}
	}
	doc.Metadata.Sections = valid
	if len(doc.Metadata.Targets) == 0 && doc.Cluster != "" && doc.Namespace != "" {
		doc.Metadata.Targets = []Target{{Cluster: doc.Cluster, Namespace: doc.Namespace}}
	}
	if doc.DocumentType != "排障手册" && len(doc.Metadata.Targets) > 1 {
		return doc, errors.New("首版只允许排障手册共享到多个目标")
	}
	for i, t := range doc.Metadata.Targets {
		doc.Metadata.Targets[i] = Target{Cluster: strings.TrimSpace(t.Cluster), Namespace: strings.TrimSpace(t.Namespace)}
	}
	if len(doc.Metadata.Targets) > 0 {
		doc.Cluster, doc.Namespace = doc.Metadata.Targets[0].Cluster, doc.Metadata.Targets[0].Namespace
	}
	doc.Environment, doc.Business = "", ""
	return doc, nil
}

func (a *Service) readyDocument(ctx context.Context, doc Document) (Document, error) {
	m := &doc.Metadata
	m.Targets = slices.Clone(m.Targets)
	if !validDate(m.ReviewBy) || m.Team == "" || doc.Title == "" || m.Applicability == "" || m.ResourceType == "" ||
		doc.DocumentType == "排障手册" && len(m.Signals) == 0 {
		return doc, errors.New("请补齐标题、责任团队、资源类型、适用条件、复核日期和手册信号")
	}
	for _, name := range requiredSections[doc.DocumentType] {
		if m.Sections[name] == "" {
			return doc, errors.New("请补齐正文中的必填章节")
		}
	}
	if a.resolve == nil || len(m.Targets) == 0 {
		return doc, errors.New("范围待确认：需要有效的实例和命名空间")
	}
	for i, target := range m.Targets {
		verified, err := a.resolve(ctx, target.Cluster, target.Namespace)
		slices.Sort(verified.Businesses)
		verified.Businesses = slices.Compact(verified.Businesses)
		if err != nil || verified.Cluster == "" || verified.Namespace == "" || verified.Environment == "" {
			return doc, errors.New("范围待确认：请核对实例、命名空间及业务归属")
		}
		m.Targets[i] = verified
	}
	seen := make(map[[2]string]bool)
	m.Targets = slices.DeleteFunc(m.Targets, func(t Target) bool {
		key := [2]string{t.Cluster, t.Namespace}
		have := seen[key]
		seen[key] = true
		return have
	})
	if m.Finding != nil && (len(m.Targets) != 1 || m.Finding.Cluster != m.Targets[0].Cluster || m.Finding.Namespace != m.Targets[0].Namespace) {
		return doc, errors.New("诊断案例须保留原始范围")
	}
	doc.Cluster, doc.Namespace, doc.Environment = m.Targets[0].Cluster, m.Targets[0].Namespace, m.Targets[0].Environment
	doc.Business = strings.Join(m.Targets[0].Businesses, ",")
	if doc.Body == "" {
		var parts []string
		for _, section := range requiredSections[doc.DocumentType] {
			parts = append(parts, m.Sections[section])
		}
		doc.Body = strings.Join(parts, "\n\n")
	}
	return doc, nil
}

func validDate(date string) bool {
	_, err := time.Parse(time.DateOnly, date)
	return err == nil && date >= time.Now().Format(time.DateOnly)
}

func (a *Service) canGovern(ctx context.Context, p identity.Principal, c Candidate) bool {
	if c.Document.Metadata.Finding != nil {
		allowed, err := a.findingAllowed(ctx, p, *c.Document.Metadata.Finding)
		if err != nil || !allowed {
			return false
		}
	}
	if a.allTargetsAllowed(ctx, p, authorization.CapabilityWikiPublish, documentTargets(c.Document)) {
		return true
	}
	return c.Owner == Owner(p) && permissions.IsAdministrator(p) && c.Document.Namespace == ""
}

func (a *Service) candidates(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		a.candidateList(w, r, p)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input struct {
		Document     Document `json:"document"`
		KnowledgeID  string   `json:"knowledgeId"`
		BaseRevision int      `json:"baseRevision"`
	}
	if !adminhttp.Decode(w, r, &input) {
		http.Error(w, "无效的草稿", 400)
		return
	}
	doc := input.Document
	doc.Metadata.Finding = nil
	doc.Metadata.SourceID = ""
	if input.KnowledgeID != "" {
		old, err := LoadDocument(ctx, a.store.DB, input.KnowledgeID)
		if err != nil || !a.allTargetsAllowed(ctx, p, authorization.CapabilityWikiPublish, documentTargets(old)) {
			http.NotFound(w, r)
			return
		}
		if old.Revision != input.BaseRevision {
			http.Error(w, "版本已变更，请重新加载", 409)
			return
		}
		doc.Metadata.Finding = old.Metadata.Finding
		doc.Metadata.SourceID = old.Metadata.SourceID
	}
	doc, err := draftDocument(doc)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id, err := randomID()
	if err != nil {
		http.Error(w, "无法创建草稿", 503)
		return
	}
	knowledgeID := input.KnowledgeID
	if knowledgeID == "" {
		knowledgeID = id
		input.BaseRevision = 0
	}
	c := Candidate{ID: id, KnowledgeID: knowledgeID, Owner: Owner(p), Status: draftStatus, Version: 1, BaseRevision: input.BaseRevision, Document: doc}
	if !a.canGovern(ctx, p, c) {
		http.Error(w, "无权维护此范围", 403)
		return
	}
	if err := insertCandidate(ctx, a.store.DB, c); err != nil {
		http.Error(w, "无法保存草稿", 503)
		return
	}
	adminhttp.WriteJSON(w, c)
}

func (a *Service) candidateList(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	rows, err := a.store.QueryContext(r.Context(), candidateQuery+" ORDER BY updated_at DESC,id")
	if err != nil {
		http.Error(w, "无法读取候选", 503)
		return
	}
	var all []Candidate
	for rows.Next() {
		c, err := readCandidate(rows)
		if err != nil {
			rows.Close()
			http.Error(w, "无法读取候选", 503)
			return
		}
		all = append(all, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "无法读取候选", 503)
		return
	}
	out := []Candidate{}
	for _, c := range all {
		if !a.canGovern(r.Context(), p, c) {
			continue
		}
		if filter := r.URL.Query().Get("status"); filter != "" && filter != c.Status {
			continue
		}
		out = append(out, c)
	}
	adminhttp.WriteJSON(w, out)
}

var candidateActions = map[string]string{
	"save": "knowledge.candidate.save", "preview": "knowledge.candidate.preview",
	"submit": "knowledge.workflow.submit", "reject": "knowledge.workflow.reject",
	"discard": "knowledge.candidate.discard", "publish": "knowledge.workflow.publish",
}

func (a *Service) candidate(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()
	var input candidateAction
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		if !adminhttp.Decode(w, r, &input) {
			http.Error(w, "无效的候选操作", 400)
			return
		}
		code, known := candidateActions[input.Action]
		if !known {
			http.Error(w, "未知的候选操作", 400)
			return
		}
		ctx = withWikiOperation(ctx, code, authorization.CapabilityWikiPublish)
		r = r.WithContext(ctx)
	default:
		w.WriteHeader(405)
		return
	}
	c, err := readCandidate(a.store.QueryRowContext(ctx, candidateQuery+" WHERE id=?", r.PathValue("id")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !a.canGovern(ctx, p, c) {
		if r.Method == http.MethodPost && a.canGovern(withWikiOperation(ctx, "knowledge.candidate.read", authorization.CapabilityWikiPublish), p, c) {
			http.Error(w, "无权执行候选操作", 403)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	if r.Method == http.MethodGet {
		adminhttp.WriteJSON(w, c)
		return
	}
	if input.Action == "publish" && c.Status == publishedStatus && input.Version+1 == c.Version {
		adminhttp.WriteJSON(w, c)
		return
	}
	if input.Version != c.Version {
		http.Error(w, "候选已更新，请重新加载", 409)
		return
	}
	switch input.Action {
	case "save", "preview", "submit":
		if c.Status != draftStatus && c.Status != rejectedStatus {
			http.Error(w, "候选当前不可编辑", 409)
			return
		}
		doc := input.Document
		doc.Metadata.Finding = c.Document.Metadata.Finding
		doc.Metadata.SourceID = c.Document.Metadata.SourceID
		if doc.Metadata.Finding != nil {
			doc.Metadata.Targets = c.Document.Metadata.Targets
			doc.Cluster, doc.Namespace = c.Document.Cluster, c.Document.Namespace
		}
		c.Document, err = draftDocument(doc)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if !a.canGovern(ctx, p, c) {
			http.Error(w, "无权维护此范围", 403)
			return
		}
		if input.Action == "preview" {
			adminhttp.WriteJSON(w, c)
			return
		}
		if input.Action == "submit" {
			c.Document, err = a.readyDocument(ctx, c.Document)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			c.Status, c.Owner, c.Reason = reviewStatus, Owner(p), ""
		} else {
			c.Status = draftStatus
		}
	case "reject":
		if c.Status != reviewStatus || strings.TrimSpace(input.Reason) == "" {
			http.Error(w, "仅可驳回待审候选，并填写原因", 400)
			return
		}
		c.Status, c.Reason = rejectedStatus, sanitizer.WikiText(input.Reason, 600)
	case "discard":
		if c.Status != draftStatus && c.Status != rejectedStatus {
			http.Error(w, "仅可放弃草稿", 409)
			return
		}
		c.Status = discardedStatus
	case "publish":
		a.publishCandidate(w, r, p, c)
		return
	default:
		http.Error(w, "未知的候选操作", 400)
		return
	}
	data, _ := json.Marshal(c.Document)
	result, err := a.store.ExecContext(ctx, "UPDATE wiki_candidates SET owner=?,status=?,version=version+1,document=?,updated_at=?,reason=? WHERE id=? AND version=?",
		c.Owner, c.Status, string(data), time.Now().Unix(), c.Reason, c.ID, c.Version)
	if err != nil {
		http.Error(w, "无法保存候选", 503)
		return
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		http.Error(w, "候选已更新，请重新加载", 409)
		return
	}
	c.Version++
	adminhttp.WriteJSON(w, c)
}
