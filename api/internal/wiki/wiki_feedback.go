package wiki

import (
	"context"
	"net/http"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

type feedbackEntry struct {
	KnowledgeID string `json:"knowledgeId"`
	Revision    int    `json:"revision"`
	Section     string `json:"section,omitempty"`
	Kind        string `json:"kind"`
	Note        string `json:"note,omitempty"`
	At          int64  `json:"at"`
	Resolved    bool   `json:"resolved"`
}

func (a *Service) wikiFeedback(w http.ResponseWriter, r *http.Request, p identity.Principal) {
	ctx := r.Context()
	id := r.PathValue("id")
	doc, err := LoadDocument(ctx, a.store.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var input feedbackEntry
		if !adminhttp.Decode(w, r, &input) || input.Revision < 1 || len(input.Note) > 1000 ||
			(input.Kind != "helpful" && input.Kind != "outdated") {
			http.Error(w, "请选择反馈类型和确切版本", 400)
			return
		}
		version, err := a.readableVersion(ctx, p, doc, input.Revision)
		if err != nil || !currentSession(ctx) {
			http.NotFound(w, r)
			return
		}
		if input.Section != "" && !hasWikiSection(version, input.Section) {
			http.Error(w, "此版本没有所选章节", 400)
			return
		}
		result, err := a.store.ExecContext(ctx, `INSERT INTO wiki_feedback(knowledge_id,revision,section,provider,subject,kind,note,updated_at)
			SELECT ?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM wiki_documents d WHERE id=? AND NOT EXISTS
				(SELECT 1 FROM wiki_publications p WHERE p.knowledge_id=d.id AND p.withdrawn=1) AND NOT EXISTS
				(SELECT 1 FROM wiki_remote_pages p JOIN wiki_disabled_sources s ON s.id=p.source_id WHERE p.knowledge_id=d.id))
			ON CONFLICT(knowledge_id,revision,section,provider,subject) DO UPDATE SET
				kind=excluded.kind,note=excluded.note,updated_at=excluded.updated_at,resolved=0`,
			id, input.Revision, input.Section, p.Provider, p.Subject, input.Kind, sanitizer.WikiText(input.Note, 1000), time.Now().Unix(), id)
		if err != nil {
			http.Error(w, "无法保存反馈，请重试", 503)
			return
		}
		if count, err := result.RowsAffected(); err != nil || count == 0 {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		if !a.allTargetsAllowed(ctx, p, authorization.CapabilityWikiPublish, documentTargets(doc)) {
			http.NotFound(w, r)
			return
		}
		entries, err := a.wikiFeedbackEntries(ctx, p, doc)
		if err != nil {
			http.Error(w, "无法读取知识反馈", 503)
			return
		}
		adminhttp.WriteJSON(w, entries)
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(405)
	}
}

func (a *Service) wikiFeedbackEntries(ctx context.Context, p identity.Principal, doc Document) ([]feedbackEntry, error) {
	rows, err := a.store.QueryContext(ctx, `SELECT revision,section,kind,note,updated_at,resolved
		FROM wiki_feedback WHERE knowledge_id=? ORDER BY updated_at DESC,revision DESC,section LIMIT 100`, doc.ID)
	if err != nil {
		return nil, err
	}
	out := []feedbackEntry{}
	for rows.Next() {
		item := feedbackEntry{KnowledgeID: doc.ID}
		if err := rows.Scan(&item.Revision, &item.Section, &item.Kind, &item.Note, &item.At, &item.Resolved); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Changes in historical visibility must also constrain feedback for that revision.
	allowed := map[int]bool{doc.Revision: true}
	visible := out[:0]
	for _, item := range out {
		ok, checked := allowed[item.Revision]
		if !checked {
			version, err := a.loadRevision(ctx, doc, item.Revision)
			ok = err == nil && a.allTargetsAllowed(ctx, p, authorization.CapabilityWikiPublish, documentTargets(version))
			allowed[item.Revision] = ok
		}
		if ok {
			visible = append(visible, item)
		}
	}
	return visible, nil
}
