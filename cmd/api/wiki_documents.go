package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
)

type wikiDocument struct {
	Revision     int    `json:"revision,omitempty"`
	ID           string `json:"knowledgeId,omitempty"`
	Cluster      string `json:"cluster"`
	Namespace    string `json:"namespace"`
	Environment  string `json:"environment"`
	Business     string `json:"business"`
	DocumentType string `json:"documentType"`
	Title        string `json:"title"`
	Body         string `json:"body,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
}

func (a *identityAdmin) wikiAllowed(r *http.Request, principal identity.Principal, capability authorization.Capability, doc wikiDocument) (bool, error) {
	decision, err := a.Authorize(r.Context(), authorization.DecisionRequest{
		Principal: principal, Capability: capability,
		Scope: authorization.NamespaceScope(doc.Cluster, doc.Namespace),
	})
	return decision.Allowed, err
}

func (a *identityAdmin) handleWikiImport(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Document wikiDocument `json:"document"`
		Publish  bool         `json:"publish"`
	}
	if !decodeAdmin(w, r, &input) {
		http.Error(w, "文档格式无效", http.StatusBadRequest)
		return
	}
	doc := input.Document
	allowed, err := a.wikiAllowed(r, principal, authorization.CapabilityWikiPublish, doc)
	if err != nil || !allowed {
		http.Error(w, "无权向此范围发布知识", http.StatusForbidden)
		return
	}
	doc, err = normalizeWikiDocument(doc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !input.Publish {
		writeAdminJSON(w, doc)
		return
	}

	hashInput, _ := json.Marshal(doc)
	digest := sha256.Sum256(hashInput)
	doc.ID = hex.EncodeToString(digest[:])
	doc.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	doc.Revision = 1
	result, err := a.store.ExecContext(r.Context(), `INSERT INTO wiki_documents
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		doc.ID, doc.Cluster, doc.Namespace, doc.Environment, doc.Business, doc.DocumentType, doc.Title, doc.Body, doc.CreatedAt)
	if err != nil {
		http.Error(w, "无法保存文档", http.StatusServiceUnavailable)
		return
	}
	inserted, err := result.RowsAffected()
	if err != nil || (inserted == 0 && a.store.QueryRowContext(r.Context(),
		"SELECT created_at FROM wiki_documents WHERE id=?", doc.ID).Scan(&doc.CreatedAt) != nil) {
		http.Error(w, "无法确认保存状态", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if inserted > 0 {
		w.WriteHeader(http.StatusCreated)
	}
	writeAdminJSON(w, doc)
}

func normalizeWikiDocument(doc wikiDocument) (wikiDocument, error) {
	scope := authorization.Scope{Cluster: doc.Cluster, Namespace: doc.Namespace}
	if scope.Validate() != nil || doc.Cluster == "*" || doc.Namespace == "" || doc.Namespace == "*" ||
		len(doc.Body) > 48<<10 || len(doc.Title) > 256 || len(doc.Environment) > 128 || len(doc.Business) > 128 {
		return wikiDocument{}, errors.New("文档字段无效或超出长度限制")
	}
	doc.ID, doc.CreatedAt, doc.Revision = "", "", 0
	doc.Body = strings.TrimSpace(sanitizer.WikiText(strings.ReplaceAll(doc.Body, "\r\n", "\n"), 48<<10))
	doc.Title = strings.TrimSpace(sanitizer.WikiText(doc.Title, 256))
	doc.Environment = strings.TrimSpace(sanitizer.WikiText(doc.Environment, 128))
	doc.Business = strings.TrimSpace(sanitizer.WikiText(doc.Business, 128))
	if doc.Title == "" {
		line, _, _ := strings.Cut(doc.Body, "\n")
		doc.Title = sanitizer.WikiText(strings.TrimSpace(strings.TrimLeft(line, "#")), 256)
	}
	if doc.DocumentType == "" {
		doc.DocumentType = "排障手册"
	}
	switch doc.DocumentType {
	case "故障复盘", "排障手册", "操作规程", "常见问题":
	default:
		return wikiDocument{}, errors.New("文档类型无效")
	}
	if doc.Title == "" || doc.Body == "" || doc.Environment == "" || doc.Business == "" {
		return wikiDocument{}, errors.New("需要正文、标题、环境及业务线")
	}
	return doc, nil
}

func (a *identityAdmin) handleWikiDocuments(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	page := 1
	if raw := query.Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > 10000 {
			http.Error(w, "页码无效", http.StatusBadRequest)
			return
		}
	}
	rows, err := a.store.QueryContext(r.Context(), `SELECT id, cluster, namespace, environment, business, document_type, title, created_at
		 ,COALESCE((SELECT MAX(revision) FROM wiki_revisions WHERE knowledge_id=id),1) FROM wiki_documents ORDER BY created_at DESC, id`)
	if err != nil {
		http.Error(w, "无法读取文档列表", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	items := make([]wikiDocument, 0, 20)
	total := 0
	options := map[string][]string{"environment": {}, "business": {}, "documentType": {}}
	seen := map[string]map[string]bool{"environment": {}, "business": {}, "documentType": {}}
	for rows.Next() {
		var doc wikiDocument
		if err := rows.Scan(&doc.ID, &doc.Cluster, &doc.Namespace, &doc.Environment, &doc.Business, &doc.DocumentType, &doc.Title, &doc.CreatedAt, &doc.Revision); err != nil {
			http.Error(w, "无法读取文档列表", http.StatusServiceUnavailable)
			return
		}
		allowed, err := a.wikiAllowed(r, principal, authorization.CapabilityWikiRead, doc)
		if err != nil {
			http.Error(w, "无法校验知识库权限", http.StatusServiceUnavailable)
			return
		}
		if !allowed {
			continue
		}
		matches := true
		for key, value := range map[string]string{"environment": doc.Environment, "business": doc.Business, "documentType": doc.DocumentType} {
			if !seen[key][value] {
				seen[key][value] = true
				options[key] = append(options[key], value)
			}
			if filter := query.Get(key); filter != "" && filter != value {
				matches = false
			}
		}
		if matches {
			total++
			if total > (page-1)*20 && len(items) < 20 {
				items = append(items, doc)
			}
		}
	}
	if rows.Err() != nil {
		http.Error(w, "无法读取文档列表", http.StatusServiceUnavailable)
		return
	}
	writeAdminJSON(w, map[string]any{"items": items, "total": total, "page": page, "pageSize": 20, "filters": options})
}

func (a *identityAdmin) handleWikiDocument(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	revision, err := strconv.Atoi(r.PathValue("revision"))
	if err != nil || revision < 1 {
		http.NotFound(w, r)
		return
	}
	doc, err := loadWikiDocument(r.Context(), a.store.DB, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	allowed, err := a.wikiAllowed(r, principal, authorization.CapabilityWikiRead, doc)
	if err != nil || !allowed {
		http.NotFound(w, r)
		return
	}
	if revision != doc.Revision {
		var encoded string
		if err := a.store.QueryRowContext(r.Context(), "SELECT document FROM wiki_revisions WHERE knowledge_id=? AND revision=?", doc.ID, revision).Scan(&encoded); err != nil ||
			json.Unmarshal([]byte(encoded), &doc) != nil {
			http.NotFound(w, r)
			return
		}
	}
	writeAdminJSON(w, doc)
}

type wikiRowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadWikiDocument(ctx context.Context, db wikiRowQuery, id string) (wikiDocument, error) {
	doc := wikiDocument{ID: id}
	err := db.QueryRowContext(ctx, `SELECT cluster, namespace, environment, business, document_type, title, body, created_at,
		COALESCE((SELECT MAX(revision) FROM wiki_revisions WHERE knowledge_id=id),1)
		FROM wiki_documents WHERE id=?`, id).Scan(&doc.Cluster, &doc.Namespace, &doc.Environment, &doc.Business, &doc.DocumentType, &doc.Title, &doc.Body, &doc.CreatedAt, &doc.Revision)
	return doc, err
}

func (a *identityAdmin) handleWikiRevision(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Revision int    `json:"expectedRevision"`
		Title    string `json:"title"`
		Body     string `json:"body"`
	}
	if !decodeAdmin(w, r, &input) || input.Revision < 1 {
		http.Error(w, "修订请求无效", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "无法创建修订", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback()
	old, err := loadWikiDocument(ctx, tx, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	allowed, err := a.wikiAllowed(r, principal, authorization.CapabilityWikiPublish, old)
	if err != nil || !allowed {
		http.NotFound(w, r)
		return
	}
	if input.Revision != old.Revision {
		http.Error(w, "文档已更新，请重新加载", http.StatusConflict)
		return
	}
	next := old
	next.Title, next.Body = input.Title, input.Body
	next, err = normalizeWikiDocument(next)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	next.ID, next.Revision, next.CreatedAt = old.ID, old.Revision+1, time.Now().UTC().Format(time.RFC3339Nano)
	for _, doc := range []wikiDocument{old, next} {
		encoded, _ := json.Marshal(doc)
		if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_revisions VALUES(?,?,?) ON CONFLICT DO NOTHING", doc.ID, doc.Revision, string(encoded)); err != nil {
			http.Error(w, "无法保存修订", http.StatusServiceUnavailable)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE wiki_documents SET title=?,body=?,created_at=? WHERE id=?", next.Title, next.Body, next.CreatedAt, next.ID); err != nil || tx.Commit() != nil {
		http.Error(w, "无法保存修订", http.StatusServiceUnavailable)
		return
	}
	writeAdminJSON(w, next)
}
