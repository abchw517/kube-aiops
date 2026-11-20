package wiki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func operationsFixture(t testing.TB) *Service {
	t.Helper()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "wiki.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a := New(store, authorization.AuthorizerFunc(func(_ context.Context, req authorization.DecisionRequest) (authorization.Decision, error) {
		return authorization.Decision{Allowed: permissions.IsAdministrator(req.Principal) ||
			req.Principal.Subject == "local:reader" && req.Capability == authorization.CapabilityWikiRead && req.Scope.Cluster == "test-a"}, nil
	}), "")
	a.SetTargetResolver(func(_ context.Context, cluster, namespace string) (Target, error) {
		switch cluster {
		case "test-a":
			return Target{Cluster: cluster, Namespace: namespace, Environment: "testing"}, nil
		case "prod-a":
			return Target{Cluster: cluster, Namespace: namespace, Environment: "production", Businesses: []string{"ops"}}, nil
		default:
			return Target{}, nil
		}
	})
	return a
}

func sampleHandbook() Document {
	return Document{Title: "PVC Pending：StorageClass 缺失", DocumentType: "排障手册", Metadata: Metadata{
		Team: "存储平台组", Applicability: "PVC Pending 并引用已退役存储类", ResourceType: "PersistentVolumeClaim",
		Signals: []string{"ProvisioningFailed"}, ReviewBy: time.Now().AddDate(0, 1, 0).Format(time.DateOnly),
		Targets:  []Target{{Cluster: "test-a", Namespace: "apps"}, {Cluster: "prod-a", Namespace: "apps"}},
		Sections: map[string]string{"conditions": "存储类缺失", "signals": "ProvisioningFailed", "checks": "确认引用目标", "recovery": "PVC Bound", "escalation": "升级至存储平台组"},
	}}
}

func TestGovernanceProductionFindingAndWithdrawal(t *testing.T) {
	a := operationsFixture(t)
	alice := identity.Principal{Provider: permissions.AdminProvider, Subject: "local:admin"}
	bob := identity.Principal{Provider: permissions.AdminProvider, Subject: "local:other-admin"}
	reader := identity.Principal{Provider: "local", Subject: "local:reader"}
	request := func(handler func(http.ResponseWriter, *http.Request, identity.Principal), p identity.Principal,
		method, path, id string, body any, expect int) string {
		t.Helper()
		var requestBody string
		if body != nil {
			payload, _ := json.Marshal(body)
			requestBody = string(payload)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(requestBody))
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		handler(w, r, p)
		if w.Code != expect {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, expect, w.Body.String())
		}
		return w.Body.String()
	}
	doc := sampleHandbook()
	path := "/api/v1/wiki/candidates"
	decode := func(raw string) Candidate {
		t.Helper()
		var c Candidate
		if err := json.Unmarshal([]byte(raw), &c); err != nil || c.ID == "" {
			t.Fatalf("invalid candidate: %s", raw)
		}
		return c
	}
	c := decode(request(a.candidates, alice, "POST", path, "", map[string]any{"document": doc}, 200))
	request(a.handleWikiDocument, reader, "GET", "/test", c.KnowledgeID, nil, 404)
	c = decode(request(a.candidate, alice, "POST", path, c.ID, candidateAction{Action: "submit", Version: 1, Document: doc}, 200))
	request(a.candidate, alice, "POST", path, c.ID, candidateAction{Action: "publish", Version: c.Version}, 403)
	raw := request(a.candidate, bob, "POST", path, c.ID, candidateAction{Action: "publish", Version: c.Version}, 200)
	first := decode(raw)
	again := decode(request(a.candidate, bob, "POST", path, c.ID, candidateAction{Action: "publish", Version: c.Version}, 200))
	if first.Document.Revision != 1 || again.Document.Revision != 1 {
		t.Fatal("publishing was not idempotent")
	}
	if raw := request(a.handleWikiDocument, reader, "GET", "/test", c.KnowledgeID, nil, 200); strings.Contains(raw, "prod-a") {
		t.Fatalf("shared detail leaked an unauthorized target: %s", raw)
	}
	key := "fixture-finding"
	a.SetFindingLookup(func(_ context.Context, cluster, id string) (finding.Finding, string, error) {
		return finding.Finding{ID: id, Cluster: cluster, Namespace: "apps", Resource: finding.ResourceRef{Kind: "PersistentVolumeClaim", Name: "reports"},
			Severity: "warning", Source: "k8sgpt", Problem: "ProvisioningFailed storage class missing"}, key, nil
	})
	if _, err := a.store.ExecContext(t.Context(), "INSERT INTO finding_reviews VALUES(?,1,?)", key, "已核对存储类名称"); err != nil {
		t.Fatal(err)
	}
	findingPath := "/api/v1/wiki/finding?cluster=test-a&id=diag-1"
	if got := request(a.findingKnowledge, alice, "GET", findingPath, "", nil, 200); !strings.Contains(got, "ProvisioningFailed") {
		t.Fatal(got)
	}
	findingDraft := map[string]any{"action": "draft", "reviewRevision": 1}
	one := decode(request(a.findingKnowledge, alice, "POST", findingPath, "", findingDraft, 200))
	two := decode(request(a.findingKnowledge, alice, "POST", findingPath, "", findingDraft, 200))
	if one.ID != two.ID || one.Document.Metadata.Finding.Revision != 1 {
		t.Fatal("Finding draft did not preserve source revision or was duplicated")
	}
	link := map[string]any{"action": "link", "reviewRevision": 1, "knowledgeId": c.KnowledgeID, "wikiRevision": 1}
	request(a.findingKnowledge, alice, "POST", findingPath, "", link, 200)
	request(a.findingKnowledge, alice, "POST", findingPath, "", link, 200)

	request(a.governDocument, bob, "POST", "/test", c.KnowledgeID, map[string]any{"action": "withdraw", "revision": 1}, 200)
	request(a.handleWikiDocument, reader, "GET", "/test", c.KnowledgeID, nil, 404)
	if got := request(a.findingKnowledge, alice, "GET", findingPath, "", nil, 200); strings.Contains(got, c.KnowledgeID) {
		t.Fatal(got)
	}
	var audit, links int
	err := a.store.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM operation_audit),(SELECT count(*) FROM wiki_finding_links)").Scan(&audit, &links)
	if err != nil || audit != 2 || links != 1 {
		t.Fatalf("got %d audit records and %d links, err %v", audit, links, err)
	}
}

func TestKnowledgeSearchPilotQueries(t *testing.T) {
	a := operationsFixture(t)
	reader := identity.Principal{Provider: "local", Subject: "local:reader"}
	querySets := map[string][]string{
		"storage": {"ProvisioningFailed", "PVC Pending", "存储类缺失", "PVC 无法绑定", "StorageClass 缺失"},
		"image":   {"ImagePullBackOff", "ErrImagePull", "镜像拉取失败", "仓库 403", "拉不到镜像"},
		"probe":   {"Readiness probe failed", "Unhealthy", "就绪探针失败", "应用不就绪", "Pod 探针失败"},
	}
	for id, words := range querySets {
		title := words[0] + " 排障"
		if _, err := a.store.ExecContext(t.Context(), "INSERT INTO wiki_documents VALUES(?, 'test-a','apps','testing','','排障手册',?,?,'2026-11-19')",
			id, title, strings.Join(words, "\n")); err != nil {
			t.Fatal(err)
		}
	}
	for id, words := range querySets {
		for _, term := range words {
			items, _, err := a.searchDocuments(t.Context(), reader, url.Values{"q": {term}}, false, nil)
			if err != nil || len(items) == 0 || items[0].ID != id {
				t.Fatalf("search %q: %+v %v", term, items, err)
			}
		}
	}
}

func BenchmarkKnowledgeSearch10000(b *testing.B) {
	a := operationsFixture(b)
	p := identity.Principal{Provider: "local", Subject: "local:reader"}
	if _, err := a.store.ExecContext(b.Context(), `WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<10000)
		INSERT INTO wiki_documents SELECT 'doc-'||i,'test-a','apps','testing','','排障手册','存储手册','PVC StorageClass ProvisioningFailed','2026-11-19' FROM n`); err != nil {
		b.Fatal(err)
	}
	query := url.Values{"q": {"ProvisioningFailed"}}
	samples := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		items, _, err := a.searchDocuments(b.Context(), p, query, false, nil)
		if err != nil || len(items) != 10000 {
			b.Fatal(fmt.Errorf("search returned %d, %w", len(items), err))
		}
		samples = append(samples, time.Since(start))
	}
	b.StopTimer()
	slices.Sort(samples)
	b.ReportMetric(float64(samples[(len(samples)-1)*95/100].Microseconds())/1000, "p95-ms")
}
