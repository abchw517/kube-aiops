package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type aggregateFindingFixture struct {
	fakeBackend
	cluster kubernetes.Cluster
	items   []finding.Finding
	readErr error
}

// newAggregateFindingFixture 为不同实例构造可重复诊断 ID 的夹具。
func newAggregateFindingFixture(id, environment, namespace string) aggregateFindingFixture {
	return aggregateFindingFixture{
		cluster: kubernetes.Cluster{ID: id, Name: id, Environment: environment, Business: "ops", Group: "shared", Status: "ready"},
		items: []finding.Finding{{
			ID: "same", Cluster: id, Namespace: namespace, Severity: finding.SeverityWarning,
			Resource: finding.ResourceRef{Kind: "Pod", Namespace: namespace, Name: id + "-pod"},
			Problem:  id + " problem", Source: finding.SourceK8sGPT, CreatedAt: "2026-11-19T02:57:48Z",
		}},
	}
}

func (b aggregateFindingFixture) Clusters() []kubernetes.Cluster {
	return []kubernetes.Cluster{b.cluster}
}

func (b aggregateFindingFixture) ListFindings(_ context.Context, query finding.Query) (finding.Page, error) {
	if b.readErr != nil {
		return finding.Page{}, b.readErr
	}
	items, err := kubernetes.FilterFindings(b.items, query.Filter)
	if err != nil {
		return finding.Page{}, err
	}
	return kubernetes.PageFindings(items, query)
}

func (b aggregateFindingFixture) SummarizeFindings(_ context.Context, filter finding.Filter) (finding.Summary, error) {
	if b.readErr != nil {
		return finding.Summary{}, b.readErr
	}
	items, err := kubernetes.FilterFindings(b.items, filter)
	if err != nil {
		return finding.Summary{}, err
	}
	return kubernetes.SummarizeFindings(items), nil
}

func (b aggregateFindingFixture) GetFinding(_ context.Context, id string) (finding.Finding, error) {
	for _, item := range b.items {
		if item.ID == id {
			return item, nil
		}
	}
	return finding.Finding{}, &kubernetes.APIError{StatusCode: http.StatusNotFound}
}

// aggregateFindingRequest 执行请求并保留失败时的响应。
func aggregateFindingRequest(t *testing.T, handler http.Handler, path string, status int) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/findings"+path, nil))
	if recorder.Code != status {
		t.Fatalf("%s: status = %d, want %d, body = %s", path, recorder.Code, status, recorder.Body.String())
	}
	return recorder
}

// decodeAggregateFindingResponse 验证并返回接口的公开结构。
func decodeAggregateFindingResponse[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAggregateFindingsFilterPaginateAndKeepClusterIdentity(t *testing.T) {
	local := newAggregateFindingFixture("local", "", "test")
	local.items = append(local.items, finding.Finding{
		ID: "older", Cluster: "local", Namespace: "test", Severity: finding.SeverityInfo,
		Resource: finding.ResourceRef{Kind: "Pod"}, Problem: "older local finding",
		Source: finding.SourceK8sGPT, CreatedAt: "2026-11-18T02:57:48Z",
	})
	prod := newAggregateFindingFixture("prod-east", "prod", "checkout")
	registry := &ClusterBackends{Backend: local, Targets: map[string]ClusterTarget{
		"prod-east": {Backend: prod},
	}}
	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), registry, time.Second)
	path := "?cluster=*&limit=1"
	var clusters []string
	for range 3 {
		response := aggregateFindingRequest(t, handler, path, http.StatusOK)
		page := decodeAggregateFindingResponse[finding.Page](t, response)
		if len(page.Items) != 1 {
			t.Fatalf("page %s: %+v", path, page)
		}
		clusters = append(clusters, page.Items[0].Cluster)
		if page.Pagination.Continue == "" {
			break
		}
		path = "?cluster=*&limit=1&continue=" + url.QueryEscape(page.Pagination.Continue)
	}
	if !slices.Equal(clusters, []string{"local", "prod-east", "local"}) {
		t.Fatalf("clusters across matching timestamps, IDs and pages = %v", clusters)
	}
	for _, test := range []struct {
		query   string
		cluster string
	}{
		{"?cluster=*&environment=prod&group=shared&business=ops&severity=warning&kind=Pod&namespace=checkout", "prod-east"},
		{"?cluster=*&environment=" + url.QueryEscape("未分组") + "&severity=info", "local"},
	} {
		page := decodeAggregateFindingResponse[finding.Page](t, aggregateFindingRequest(t, handler, test.query, http.StatusOK))
		summary := decodeAggregateFindingResponse[finding.Summary](t, aggregateFindingRequest(t, handler, "/summary"+test.query, http.StatusOK))
		if len(page.Items) != 1 || page.Items[0].Cluster != test.cluster || summary.Total != 1 || summary.ByKind["Pod"] != 1 ||
			summary.BySeverity[page.Items[0].Severity] != 1 || summary.ByNamespace[page.Items[0].Namespace] != 1 {
			t.Fatalf("scope %s: page=%+v, summary=%+v", test.query, page, summary)
		}
	}
	legacy := decodeAggregateFindingResponse[finding.Page](t, aggregateFindingRequest(t, handler, "", http.StatusOK))
	if len(legacy.Items) != 2 || legacy.Items[0].Cluster != "local" || legacy.Items[1].Cluster != "local" {
		t.Fatalf("omitted cluster must remain local: %+v", legacy)
	}
	for _, cluster := range []string{"local", "prod-east"} {
		response := aggregateFindingRequest(t, handler, "/same?cluster="+cluster, http.StatusOK)
		detail := decodeAggregateFindingResponse[finding.Finding](t, response)
		if detail.Cluster != cluster {
			t.Fatalf("detail scope %s returned %+v", cluster, detail)
		}
	}
	aggregateFindingRequest(t, handler, "?cluster=*&limit=201", http.StatusBadRequest)
	aggregateFindingRequest(t, handler, "?cluster=*&continue=invalid", http.StatusBadRequest)
}

func TestAggregateFindingsRespectCollectionAndNamespaceGrants(t *testing.T) {
	local := newAggregateFindingFixture("local", "test", "allowed")
	denied := local.items[0]
	denied.ID, denied.Namespace, denied.Resource.Namespace = "hidden-result", "restricted", "restricted"
	local.items = append(local.items, denied)
	undiscoverable := newAggregateFindingFixture("undiscoverable", "hidden", "allowed")
	undiscoverable.readErr = errors.New("undiscoverable cluster was queried")
	collectionDenied := newAggregateFindingFixture("collection-denied", "test", "allowed")
	collectionDenied.readErr = errors.New("cluster without findings access was queried")
	registry := &ClusterBackends{Backend: local, Targets: map[string]ClusterTarget{
		"undiscoverable": {Backend: undiscoverable}, "collection-denied": {Backend: collectionDenied},
	}}
	base := authorization.Policy{Version: authorization.PolicyVersionV1}
	policy, err := base.WithRoles([]authorization.Role{
		{RoleSpec: authorization.RoleSpec{
			Name: "Reviewer", Subjects: []string{"local:reviewer"}, Cluster: "local", Namespaces: []string{"allowed"},
			Modules: []authorization.Capability{authorization.CapabilityClustersList, authorization.CapabilityFindingsList, authorization.CapabilityFindingsSummary},
		}},
		{RoleSpec: authorization.RoleSpec{
			Name: "FindingOnly", Subjects: []string{"local:reviewer"}, Cluster: "undiscoverable", Namespaces: []string{"allowed"},
			Modules: []authorization.Capability{authorization.CapabilityFindingsList, authorization.CapabilityFindingsSummary},
		}},
		{RoleSpec: authorization.RoleSpec{
			Name: "DiscoveryOnly", Subjects: []string{"local:reviewer"}, Cluster: "collection-denied", Namespaces: []string{"allowed"},
			Modules: []authorization.Capability{authorization.CapabilityClustersList},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithOptions(slog.New(slog.NewTextHandler(io.Discard, nil)), registry, time.Second, HandlerOptions{
		Authenticator: identity.AuthenticatorFunc(func(context.Context, *http.Request) (identity.Principal, error) {
			return identity.Principal{Subject: "local:reviewer", Provider: "local"}, nil
		}), Authorizer: policy,
	})
	for _, endpoint := range []string{"", "/summary"} {
		response := aggregateFindingRequest(t, handler, endpoint+"?cluster=*", http.StatusOK)
		body := response.Body.String()
		if !strings.Contains(body, "allowed") || strings.Contains(body, "restricted") ||
			strings.Contains(body, "undiscoverable") || strings.Contains(body, "collection-denied") {
			t.Fatalf("%s includes unauthorized findings: %s", endpoint, body)
		}
		empty := aggregateFindingRequest(t, handler, endpoint+"?cluster=*&environment=hidden", http.StatusOK)
		if strings.Contains(empty.Body.String(), "allowed") {
			t.Fatalf("%s widened an unavailable environment: %s", endpoint, empty.Body.String())
		}
		aggregateFindingRequest(t, handler, endpoint+"?cluster=*&namespace=restricted", http.StatusForbidden)
	}
}

func TestAggregateFindingsReportsSourceFailure(t *testing.T) {
	local := newAggregateFindingFixture("local", "test", "allowed")
	prod := newAggregateFindingFixture("prod-east", "prod", "allowed")
	prod.readErr = errors.New("diagnostic source unavailable")
	registry := &ClusterBackends{Backend: local, Targets: map[string]ClusterTarget{
		"prod-east": {Backend: prod},
	}}
	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), registry, time.Second)
	for _, endpoint := range []string{"", "/summary"} {
		response := aggregateFindingRequest(t, handler, endpoint+"?cluster=*", http.StatusBadGateway)
		if strings.Contains(response.Body.String(), "local problem") || strings.Contains(response.Body.String(), "diagnostic source unavailable") {
			t.Fatalf("%s exposed partial data or backend error details: %s", endpoint, response.Body.String())
		}
		aggregateFindingRequest(t, handler, endpoint+"?cluster=*&environment=test", http.StatusOK)
	}
}
