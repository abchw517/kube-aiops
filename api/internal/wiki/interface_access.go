package wiki

import (
	"context"
	"net/http"

	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type wikiOperationKey struct{}

type wikiOperation struct {
	key    string
	legacy authorization.Capability
}

func withWikiOperation(ctx context.Context, code string, legacy authorization.Capability) context.Context {
	return context.WithValue(ctx, wikiOperationKey{}, wikiOperation{code, legacy})
}

func forWikiMethod(r *http.Request, get, post, put, remove string, legacy authorization.Capability) *http.Request {
	code := ""
	switch r.Method {
	case http.MethodGet:
		code = get
	case http.MethodPost:
		code = post
	case http.MethodPut:
		code = put
	case http.MethodDelete:
		code = remove
	}
	return r.WithContext(withWikiOperation(r.Context(), code, legacy))
}

func (a *Service) permit(ctx context.Context, p identity.Principal, code string, legacy authorization.Capability, scope authorization.Scope) (bool, error) {
	if auth, ok := a.Authorizer.(interface {
		AuthorizeOperation(context.Context, identity.Principal, string, authorization.Capability, authorization.Scope, bool) (bool, error)
	}); ok && code != "" {
		return auth.AuthorizeOperation(ctx, p, code, legacy, scope, false)
	}
	decision, err := a.Authorize(ctx, authorization.DecisionRequest{Principal: p, Capability: legacy, Scope: scope})
	return decision.Allowed, err
}

func (a *Service) permitTarget(ctx context.Context, p identity.Principal, legacy authorization.Capability, scope authorization.Scope) (bool, error) {
	code := ""
	switch legacy {
	case authorization.CapabilityWikiRead:
		code = catalog.DocumentRead
	case authorization.CapabilityFindingsRead:
		code = catalog.FindingRead
	}
	if operation, ok := ctx.Value(wikiOperationKey{}).(wikiOperation); ok && operation.legacy == legacy && operation.key != "" {
		code = operation.key
	}
	return a.permit(ctx, p, code, legacy, scope)
}
