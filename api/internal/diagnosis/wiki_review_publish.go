package diagnosis

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
	"net/http"
	"strconv"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type wikiBaselineKey struct{}

func wikiReviewOwner(ctx context.Context) string {
	p, _ := identity.PrincipalFromContext(ctx)
	data, _ := json.Marshal([2]string{p.Provider, p.Subject})
	return string(data)
}

func (f *Review) createWikiReviewDraft(ctx context.Context, w http.ResponseWriter, key string, current int, conclusion string) {
	now := time.Now()
	_, _ = f.store.ExecContext(ctx, "DELETE FROM wiki_review_drafts WHERE expires_at<=?", now.Unix())
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		http.Error(w, "无法生成结论草稿", http.StatusServiceUnavailable)
		return
	}
	id := hex.EncodeToString(random)
	if _, err := f.store.ExecContext(ctx, "INSERT INTO wiki_review_drafts VALUES(?,?,?,?,?,?)", id, key, wikiReviewOwner(ctx), current, conclusion, now.Add(time.Hour).Unix()); err != nil {
		http.Error(w, "无法保存结论草稿", http.StatusServiceUnavailable)
		return
	}
	adminhttp.WriteJSON(w, map[string]string{"draftId": id, "conclusion": conclusion})
}

func (f *Review) confirmWikiReview(ctx context.Context, w http.ResponseWriter, item finding.Finding, key string, current int, draftID string) {
	if item.Namespace == "" {
		http.Error(w, "此诊断缺少 Wiki 发布所需的命名空间", http.StatusBadRequest)
		return
	}
	tx, err := f.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "无法保存结论", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback()
	var conclusion string
	if err := tx.QueryRowContext(ctx, "SELECT conclusion FROM wiki_review_drafts WHERE id=? AND finding_key=? AND owner=? AND revision=? AND expires_at>?",
		draftID, key, wikiReviewOwner(ctx), current, time.Now().Unix()).Scan(&conclusion); err != nil {
		http.Error(w, "草稿已变更或过期", http.StatusConflict)
		return
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO finding_reviews VALUES(?,?,?) ON CONFLICT DO NOTHING", key, current+1, conclusion)
	if err != nil {
		http.Error(w, "无法保存结论", http.StatusServiceUnavailable)
		return
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		http.Error(w, "诊断已更新", http.StatusConflict)
		return
	}
	doc, err := wikiReviewDocument(item, conclusion)
	if err != nil {
		http.Error(w, "无法生成知识条目", http.StatusBadRequest)
		return
	}
	doc, err = upsertReviewDocument(ctx, tx, key, doc)
	if err != nil {
		http.Error(w, "无法发布知识", http.StatusServiceUnavailable)
		return
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_review_drafts WHERE id=?", draftID); err != nil || tx.Commit() != nil {
		http.Error(w, "无法确认发布", http.StatusServiceUnavailable)
		return
	}
	adminhttp.WriteJSON(w, map[string]any{"conclusion": conclusion, "revision": current + 1, "knowledgeId": doc.ID})
}

func wikiReviewDocument(item finding.Finding, conclusion string) (wiki.Document, error) {
	return wiki.NormalizeDocument(wiki.Document{
		Cluster: item.Cluster, Namespace: item.Namespace, Environment: "集群诊断", Business: item.Cluster, DocumentType: "故障复盘",
		Title: "故障复盘：" + item.Resource.Kind + " " + item.Resource.Name, Body: "## 故障现象\n" + item.Problem + "\n\n## 最终诊断\n" + conclusion,
	})
}

func (f *Review) publishSavedReview(ctx context.Context, w http.ResponseWriter, item finding.Finding, key string, saved findingRevision, publish bool, previewID string) {
	doc, err := wikiReviewDocument(item, saved.Conclusion)
	if err != nil {
		http.Error(w, "无法生成知识预览", http.StatusBadRequest)
		return
	}
	data, _ := json.Marshal(doc)
	digest := sha256.Sum256(append([]byte(key+"\x00"+strconv.Itoa(saved.Revision)+"\x00"), data...))
	id := hex.EncodeToString(digest[:])
	if !publish {
		adminhttp.WriteJSON(w, map[string]any{"previewId": id, "document": doc})
		return
	}
	if previewID != id {
		http.Error(w, "预览已变更，请重新预览", http.StatusConflict)
		return
	}
	tx, err := f.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "无法发布知识", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, "SELECT MAX(revision) FROM finding_reviews WHERE finding_key=?", key).Scan(&current); err != nil {
		http.Error(w, "无法检查结论版本", http.StatusServiceUnavailable)
		return
	}
	if current != saved.Revision {
		http.Error(w, "诊断已更新，请重新预览", http.StatusConflict)
		return
	}
	doc, err = upsertReviewDocument(ctx, tx, key, doc)
	if err != nil || tx.Commit() != nil {
		http.Error(w, "无法发布知识", http.StatusServiceUnavailable)
		return
	}
	adminhttp.WriteJSON(w, doc)
}

func upsertReviewDocument(ctx context.Context, tx *sql.Tx, key string, doc wiki.Document) (wiki.Document, error) {
	identifier := sha256.Sum256([]byte("finding\x00" + key))
	doc.ID, doc.CreatedAt = hex.EncodeToString(identifier[:]), time.Now().UTC().Format(time.RFC3339Nano)
	doc.Revision = 1
	old, err := wiki.LoadDocument(ctx, tx, doc.ID)
	if err == nil {
		if old.Title == doc.Title && old.Body == doc.Body {
			return old, nil
		}
		doc.Revision = old.Revision + 1
		for _, version := range []wiki.Document{old, doc} {
			data, _ := json.Marshal(version)
			if _, err := tx.ExecContext(ctx, "INSERT INTO wiki_revisions VALUES(?,?,?) ON CONFLICT DO NOTHING", version.ID, version.Revision, string(data)); err != nil {
				return wiki.Document{}, err
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return wiki.Document{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO wiki_documents VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id)
		DO UPDATE SET title=excluded.title,body=excluded.body,created_at=excluded.created_at`,
		doc.ID, doc.Cluster, doc.Namespace, doc.Environment, doc.Business, doc.DocumentType, doc.Title, doc.Body, doc.CreatedAt)
	return doc, err
}
