package httpapi

import (
	"context"
	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

type namedClusterBackend struct {
	fakeBackend
	id string
}

func (b namedClusterBackend) Clusters() []kubernetes.Cluster {
	return []kubernetes.Cluster{{ID: b.id, Name: b.id, Environment: "test", Status: "unknown"}}
}

func TestFindingLookupUsesSelectedCluster(t *testing.T) {
	backend := func(id string) namedClusterBackend {
		return namedClusterBackend{fakeBackend{findingItem: finding.Finding{ID: "same", Cluster: id, Severity: "warning",
			Problem: id + " problem", Source: "k8sgpt", Resource: finding.ResourceRef{Kind: "Pod", Name: "api", Namespace: "default"}}}, id}
	}
	registry := &ClusterBackends{Backend: backend("local"), Targets: map[string]ClusterTarget{
		"dev": {Backend: backend("dev")}, "uat": {Backend: backend("uat")},
	}}
	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), registry, time.Second)
	for _, test := range []struct {
		query, want string
		status      int
	}{
		{"", "local problem", 200}, {"?cluster=dev", "dev problem", 200}, {"?cluster=uat", "uat problem", 200},
		{"?cluster=unknown", "CLUSTER_NOT_FOUND", 404},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/findings/same"+test.query, nil))
		if w.Code != test.status || !strings.Contains(w.Body.String(), test.want) {
			t.Fatalf("%s: %d %s", test.query, w.Code, w.Body.String())
		}
	}
	policy, err := authorization.ParsePolicyJSON([]byte(`{"version":"v1","rules":[
		{"effect":"allow","subjects":["tester"],"capabilities":["clusters:list"],"scopes":[{"cluster":"*"}]},
		{"effect":"deny","subjects":["tester"],"capabilities":["clusters:list"],"scopes":[{"cluster":"uat"}]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	protected := NewHandlerWithOptions(slog.New(slog.NewTextHandler(io.Discard, nil)), registry, time.Second, HandlerOptions{
		Authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
			return identity.Principal{Subject: "tester", Provider: "test"}, nil
		}), Authorizer: policy,
	})
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), `"uat"`) || !strings.Contains(w.Body.String(), `"dev"`) {
		t.Fatalf("cluster discovery: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/findings/same?cluster=uat", nil))
	if w.Code != 403 {
		t.Fatalf("hidden cluster lookup: %d %s", w.Code, w.Body.String())
	}

}
