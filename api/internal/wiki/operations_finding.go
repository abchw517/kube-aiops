package wiki

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

type FindingLookup func(context.Context, string, string) (finding.Finding, string, error)

type findingLink struct {
	KnowledgeID  string `json:"knowledgeId"`
	Title        string `json:"title,omitempty"`
	WikiRevision int    `json:"wikiRevision"`
	FindingSource
}

func (a *Service) findingKnowledge(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()
	cluster := r.URL.Query().Get("cluster")
	if cluster == "" {
		cluster = "local"
	}
	id := r.URL.Query().Get("id")
	if a.find == nil || len(id) == 0 || len(id) > 253 {
		http.NotFound(w, r)
		return
	}
	item, key, err := a.find(ctx, cluster, id)
	if err != nil || item.Cluster != cluster {
		http.NotFound(w, r)
		return
	}
	source := FindingSource{Cluster: cluster, Namespace: item.Namespace, ID: item.ID}
	if ok, err := a.findingAllowed(ctx, p, source); err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	safe, err := sanitizer.Default().Finding(item)
	if err != nil {
		http.Error(w, "无法读取诊断", 503)
		return
	}
	if r.Method == http.MethodGet {
		target := Target{Cluster: source.Cluster, Namespace: source.Namespace}
		status := "ready"
		list := []Document{}
		if source.Namespace != "" && a.resolve != nil {
			actual, resolveErr := a.currentTarget(ctx, source.Cluster, source.Namespace)
			if resolveErr != nil {
				http.Error(w, "无法解析诊断范围，请重试", 503)
				return
			}
			if actual.Environment != "" {
				target = actual
				values := url.Values{"q": {safe.Problem + " " + safe.Details}, "resourceType": {safe.Resource.Kind}}
				matchFinding := true
				if keyword := strings.TrimSpace(r.URL.Query().Get("q")); keyword != "" {
					if len(keyword) > 512 {
						http.Error(w, "搜索词过长", 400)
						return
					}
					values = url.Values{"q": {keyword}, "documentType": {"排障手册"}}
					matchFinding = false
				}
				if list, _, err = a.searchDocuments(ctx, p, values, matchFinding, &target); err != nil {
					http.Error(w, "无法获取相关知识，请重试", 503)
					return
				}
			} else {
				status = "unresolved"
			}
		} else {
			status = "unresolved"
		}
		list = list[:min(3, len(list))]
		for i := range list {
			list[i].Body = ""
			list[i].Metadata.Sections = nil
		}
		links, err := a.findingLinks(ctx, p, key)
		if err != nil {
			http.Error(w, "无法加载知识关联", 503)
			return
		}
		adminhttp.WriteJSON(w, map[string]any{"items": list, "links": links, "status": status, "canDraft": a.canDraftFinding(ctx, p, source)})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input struct {
		Action       string `json:"action"`
		Revision     int    `json:"reviewRevision"`
		KnowledgeID  string `json:"knowledgeId"`
		WikiRevision int    `json:"wikiRevision"`
	}
	if !adminhttp.Decode(w, r, &input) || input.Revision < 1 {
		http.Error(w, "请先保存复核结论", 400)
		return
	}
	if !a.canDraftFinding(ctx, p, source) {
		http.Error(w, "无权维护此范围", 403)
		return
	}
	var conclusion string
	if err := a.store.QueryRowContext(ctx, "SELECT revision,conclusion FROM finding_reviews WHERE finding_key=? ORDER BY revision DESC LIMIT 1", key).Scan(&source.Revision, &conclusion); err != nil || source.Revision != input.Revision {
		http.Error(w, "复核已更新，请重新加载", 409)
		return
	}

	switch input.Action {
	case "draft":
		a.draftFinding(w, r, p, safe, key, source, conclusion)
	case "link":
		a.linkFinding(w, r, p, key, source, input.KnowledgeID, input.WikiRevision)
	default:
		http.Error(w, "未知的沉淀操作", 400)
	}
}

func (a *Service) canDraftFinding(ctx context.Context, p identity.Principal, src FindingSource) bool {
	if src.Namespace == "" {
		return permissions.IsAdministrator(p)
	}
	return a.allTargetsAllowed(ctx, p, authorization.CapabilityWikiPublish, []Target{{Cluster: src.Cluster, Namespace: src.Namespace}})
}

func (a *Service) draftFinding(w http.ResponseWriter, r *http.Request, p identity.Principal, item finding.Finding, key string, source FindingSource, conclusion string) {
	ctx := r.Context()
	digest := sha256.Sum256([]byte("finding\x00" + key))
	id := hex.EncodeToString(digest[:])
	base, err := LoadDocument(ctx, a.store.DB, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "无法读取知识版本", 503)
		return
	}
	candidateBase := base.Revision
	if previous := base.Metadata.Finding; !base.Withdrawn && previous != nil && previous.Revision == source.Revision {
		candidateBase--
	}
	candidateKey := sha256.Sum256([]byte(id + "\x00" + strconv.Itoa(source.Revision) + "\x00" + strconv.Itoa(candidateBase)))
	doc := Document{Cluster: source.Cluster, Namespace: source.Namespace, DocumentType: "故障复盘", Title: "故障复盘：" + item.Resource.Kind + " " + item.Resource.Name,
		Metadata: Metadata{ResourceType: item.Resource.Kind, Finding: &source, Sections: map[string]string{
			"impact": strings.TrimSpace(item.CreatedAt + " " + item.Problem), "conclusion": conclusion,
		}}}
	doc, err = draftDocument(doc)
	if err != nil {
		http.Error(w, "无法生成草稿", 400)
		return
	}
	c := Candidate{ID: hex.EncodeToString(candidateKey[:]), KnowledgeID: id, Owner: Owner(p), Status: draftStatus, Version: 1, BaseRevision: base.Revision, Document: doc}
	if err := insertCandidate(ctx, a.store.DB, c); err != nil {
		http.Error(w, "无法保存草稿", 503)
		return
	}
	c, err = readCandidate(a.store.QueryRowContext(ctx, candidateQuery+" WHERE id=?", c.ID))
	if err != nil {
		http.Error(w, "无法读取草稿", 503)
		return
	}
	adminhttp.WriteJSON(w, c)
}

func (a *Service) linkFinding(w http.ResponseWriter, r *http.Request, p identity.Principal, key string, source FindingSource, id string, wikiRevision int) {
	ctx := r.Context()
	doc, err := LoadDocument(ctx, a.store.DB, id)
	if err != nil || doc.DocumentType != "排障手册" {
		http.NotFound(w, r)
		return
	}
	target := Target{Cluster: source.Cluster, Namespace: source.Namespace}
	if ok, err := a.canRead(ctx, p, doc, &target, false); err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "无法关联知识", 503)
		return
	}
	defer tx.Rollback()
	saved, err := LoadDocument(ctx, tx, id)
	var current int
	revErr := tx.QueryRowContext(ctx, "SELECT MAX(revision) FROM finding_reviews WHERE finding_key=?", key).Scan(&current)
	if err != nil || revErr != nil || saved.Withdrawn || saved.Revision != wikiRevision || current != source.Revision {
		http.Error(w, "复核或知识版本已更新，请重新加载", 409)
		return
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wiki_finding_links VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		key, id, wikiRevision, source.Revision, source.Cluster, source.Namespace, source.ID); err != nil {
		http.Error(w, "无法关联知识", 503)
		return
	}
	out := findingLink{KnowledgeID: id, Title: doc.Title, FindingSource: source}
	if err := tx.QueryRowContext(ctx, "SELECT wiki_revision,review_revision FROM wiki_finding_links WHERE finding_key=? AND knowledge_id=?", key, id).
		Scan(&out.WikiRevision, &out.Revision); err != nil || tx.Commit() != nil {
		http.Error(w, "无法关联知识", 503)
		return
	}
	adminhttp.WriteJSON(w, out)
}

func (a *Service) findingLinks(ctx context.Context, p identity.Principal, key string) ([]findingLink, error) {
	rows, err := a.store.QueryContext(ctx, "SELECT knowledge_id,wiki_revision,review_revision,cluster,namespace,finding_id FROM wiki_finding_links WHERE finding_key=? ORDER BY knowledge_id", key)
	if err != nil {
		return nil, err
	}
	out, err := readLinks(rows)
	if err != nil {
		return nil, err
	}
	filtered := []findingLink{}
	for _, link := range out {
		doc, err := LoadDocument(ctx, a.store.DB, link.KnowledgeID)
		if err != nil {
			continue
		}
		target := Target{Cluster: link.Cluster, Namespace: link.Namespace}
		if ok, err := a.canRead(ctx, p, doc, &target, false); err == nil && ok {
			link.Title = doc.Title
			filtered = append(filtered, link)
		}
	}
	return filtered, nil
}

func readLinks(rows *sql.Rows) ([]findingLink, error) {
	defer rows.Close()
	var out []findingLink
	for rows.Next() {
		var link findingLink
		if err := rows.Scan(&link.KnowledgeID, &link.WikiRevision, &link.Revision, &link.Cluster, &link.Namespace, &link.ID); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (a *Service) knowledgeLinks(ctx context.Context, p identity.Principal, id string) ([]findingLink, error) {
	rows, err := a.store.QueryContext(ctx, "SELECT knowledge_id,wiki_revision,review_revision,cluster,namespace,finding_id FROM wiki_finding_links WHERE knowledge_id=? ORDER BY finding_id,cluster", id)
	if err != nil {
		return nil, err
	}
	out, err := readLinks(rows)
	if err != nil {
		return nil, err
	}
	filtered := []findingLink{}
	for _, link := range out {
		if ok, err := a.findingAllowed(ctx, p, link.FindingSource); err == nil && ok {
			filtered = append(filtered, link)
		}
	}
	return filtered, nil
}
