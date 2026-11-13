package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/identity"
)

type scopedFindingBackend struct{ fakeBackend }

func (scopedFindingBackend) ListFindings(_ context.Context, query finding.Query) (finding.Page, error) {
	page := finding.Page{Items: []finding.Finding{}, Pagination: finding.Pagination{Limit: 50}}
	for _, ns := range []string{"allowed", "restricted"} {
		item := finding.Finding{ID: ns, Cluster: "local", Namespace: ns, Severity: "warning", Resource: finding.ResourceRef{Kind: "Pod", Namespace: ns, Name: ns}, Problem: "issue", Source: "k8sgpt"}
		if query.Visible == nil {
			page.Items = append(page.Items, item)
			continue
		}
		allowed, err := query.Visible(item)
		if err != nil {
			return finding.Page{}, err
		}
		if allowed {
			page.Items = append(page.Items, item)
		}
	}
	return page, nil
}

func (b scopedFindingBackend) SummarizeFindings(ctx context.Context, filter finding.Filter) (finding.Summary, error) {
	page, err := b.ListFindings(ctx, finding.Query{Filter: filter})
	summary := finding.Summary{Total: len(page.Items), ByNamespace: map[string]int{}}
	for _, item := range page.Items {
		summary.ByNamespace[item.Namespace]++
	}
	return summary, err
}

func TestUnfilteredFindingResponsesRespectNamespaceDeny(t *testing.T) {
	policy, err := authorization.ParsePolicyJSON([]byte(`{"version":"v1","rules":[
 {"effect":"allow","subjects":["local:reviewer"],"capabilities":["findings:list","findings:summary"],"scopes":[{"cluster":"local"},{"cluster":"local","namespace":"*"}]},
 {"effect":"deny","subjects":["local:reviewer"],"capabilities":["findings:list","findings:summary"],"scopes":[{"cluster":"local","namespace":"restricted"}]}
 ]}`))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithOptions(slog.New(slog.NewTextHandler(io.Discard, nil)), scopedFindingBackend{}, time.Second, HandlerOptions{
		Authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
			return identity.Principal{Subject: "local:reviewer", Provider: "local"}, nil
		}), Authorizer: policy,
	})
	for _, path := range []string{"/api/v1/findings", "/api/v1/findings/summary"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" ||
			strings.Contains(w.Body.String(), "restricted") || !strings.Contains(w.Body.String(), "allowed") {
			t.Fatalf("%s status=%d headers=%v body=%s", path, w.Code, w.Header(), w.Body.String())
		}
	}
}
