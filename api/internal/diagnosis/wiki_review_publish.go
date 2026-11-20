package diagnosis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"net/http"
	"time"

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
