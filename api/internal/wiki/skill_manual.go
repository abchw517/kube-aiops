package wiki

import (
	"context"
	"database/sql"

	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

// SkillManual restricts conversions and runtime references to currently published manuals.
func (a *Service) SkillManual(ctx context.Context, p identity.Principal, id string, revision int, cluster, namespace string) (Document, error) {
	doc, err := LoadDocument(ctx, a.store.DB, id)
	if err != nil {
		return Document{}, err
	}
	if doc.DocumentType != "排障手册" || (revision > 0 && doc.Revision != revision) {
		return Document{}, sql.ErrNoRows
	}
	target := &Target{Cluster: cluster, Namespace: namespace}
	allowed, err := a.canRead(withTargetCache(ctx), p, doc, target, true)
	if err != nil {
		return Document{}, err
	}
	if !allowed {
		return Document{}, sql.ErrNoRows
	}
	return doc, nil
}
