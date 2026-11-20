package wiki

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

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

type Document struct {
	Revision        int           `json:"revision,omitempty"`
	ID              string        `json:"knowledgeId,omitempty"`
	Cluster         string        `json:"cluster"`
	Namespace       string        `json:"namespace"`
	Environment     string        `json:"environment"`
	Business        string        `json:"business"`
	DocumentType    string        `json:"documentType"`
	Title           string        `json:"title"`
	Body            string        `json:"body,omitempty"`
	CreatedAt       string        `json:"createdAt,omitempty"`
	Metadata        Metadata      `json:"metadata"`
	Match           string        `json:"matchReason,omitempty"`
	Withdrawn       bool          `json:"withdrawn,omitempty"`
	Links           []findingLink `json:"links,omitempty"`
	ScopePending    bool          `json:"scopePending,omitempty"`
	CurrentRevision int           `json:"currentRevision,omitempty"`
	PendingFeedback int           `json:"pendingFeedback,omitempty"`
}

func (a *Service) wikiAllowed(r *http.Request, principal identity.Principal, capability authorization.Capability, doc Document) (bool, error) {
	return a.permitTarget(r.Context(), principal, capability, authorization.NamespaceScope(doc.Cluster, doc.Namespace))
}

func (a *Service) handleWikiImport(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Document Document `json:"document"`
		Publish  bool     `json:"publish"`
	}
	if !adminhttp.Decode(w, r, &input) {
		http.Error(w, "文档格式无效", http.StatusBadRequest)
		return
	}
	doc := input.Document
	allowed, err := a.wikiAllowed(r, principal, authorization.CapabilityWikiPublish, doc)
	if err != nil || !allowed {
		http.Error(w, "无权向此范围发布知识", http.StatusForbidden)
		return
	}
	doc, err = NormalizeDocument(doc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !input.Publish {
		adminhttp.WriteJSON(w, doc)
		return
	}

	draftRequired(w)
}

func NormalizeDocument(doc Document) (Document, error) {
	scope := authorization.Scope{Cluster: doc.Cluster, Namespace: doc.Namespace}
	if scope.Validate() != nil || doc.Cluster == "*" || doc.Namespace == "" || doc.Namespace == "*" ||
		len(doc.Body) > 48<<10 || len(doc.Title) > 256 || len(doc.Environment) > 128 || len(doc.Business) > 128 {
		return Document{}, errors.New("文档字段无效或超出长度限制")
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
		return Document{}, errors.New("文档类型无效")
	}
	if doc.Title == "" || doc.Body == "" {
		return Document{}, errors.New("需要正文和标题")
	}
	return doc, nil
}

func (a *Service) handleWikiDocuments(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
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
	filtersForCursor := r.URL.Query()
	filtersForCursor.Del("page")
	filtersForCursor.Del("cursor")
	digest := sha256.Sum256([]byte(principal.Subject + "\x00" + filtersForCursor.Encode()))
	cursorKey := hex.EncodeToString(digest[:8])
	if cursor := query.Get("cursor"); cursor != "" {
		key, number, ok := strings.Cut(cursor, ".")
		next, err := strconv.Atoi(number)
		if !ok || key != cursorKey || next < 1 || next > 10000 || err != nil {
			http.Error(w, "搜索条件已变更，请从首页重新查询", 400)
			return
		}
		page = next
	}
	if len(query.Get("q")) > 512 {
		http.Error(w, "搜索词过长", 400)
		return
	}
	results, options, err := a.searchDocuments(r.Context(), principal, query, false, nil)
	if err != nil {
		http.Error(w, "无法读取知识库，请重试", 503)
		return
	}
	start := min(len(results), (page-1)*20)
	end := min(len(results), start+20)
	items := results[start:end]
	for i := range items {
		items[i].Body = ""
		items[i].Metadata.Sections = nil
	}
	out := map[string]any{"items": items, "total": len(results), "page": page, "pageSize": 20, "filters": options}
	if end < len(results) {
		out["nextCursor"] = cursorKey + "." + strconv.Itoa(page+1)
	}
	adminhttp.WriteJSON(w, out)
}

func (a *Service) handleWikiDocument(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	current, err := LoadDocument(ctx, a.store.DB, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	revision := current.Revision
	if raw := r.PathValue("revision"); raw != "" {
		revision, err = strconv.Atoi(raw)
		if err != nil || revision < 1 {
			http.NotFound(w, r)
			return
		}
	}
	doc, err := a.readableVersion(ctx, principal, current, revision)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	doc.CurrentRevision = current.Revision
	a.projectTargets(ctx, principal, &doc)
	doc.Links, _ = a.knowledgeLinks(ctx, principal, doc.ID)
	adminhttp.WriteJSON(w, doc)
}

func (a *Service) loadRevision(ctx context.Context, current Document, revision int) (Document, error) {
	if revision == current.Revision {
		return current, nil
	}
	var snapshot Document
	var encoded string
	if err := a.store.QueryRowContext(ctx, "SELECT document FROM wiki_revisions WHERE knowledge_id=? AND revision=?", current.ID, revision).Scan(&encoded); err != nil {
		return Document{}, err
	}
	err := json.Unmarshal([]byte(encoded), &snapshot)
	return snapshot, err
}

func (a *Service) readableVersion(ctx context.Context, principal identity.Principal, current Document, revision int) (Document, error) {
	allowed, err := a.canRead(ctx, principal, current, nil, false)
	if err != nil || !allowed || current.Withdrawn {
		return Document{}, sql.ErrNoRows
	}
	doc, err := a.loadRevision(ctx, current, revision)
	if err != nil {
		return Document{}, err
	}
	if revision != current.Revision {
		allowed, err = a.canRead(ctx, principal, doc, nil, false)
		if err != nil || !allowed {
			return Document{}, sql.ErrNoRows
		}
	}
	return doc, nil
}

type wikiRowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func LoadDocument(ctx context.Context, db wikiRowQuery, id string) (Document, error) {
	doc := Document{ID: id}
	err := readPublished(db.QueryRowContext(ctx, publishedQuery+" WHERE d.id=?", id), &doc)
	return doc, err
}

func (a *Service) handleWikiRevision(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	doc, err := LoadDocument(r.Context(), a.store.DB, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	allowed, _ := a.wikiAllowed(r, principal, authorization.CapabilityWikiPublish, doc)
	if !allowed {
		http.NotFound(w, r)
		return
	}
	draftRequired(w)
}
