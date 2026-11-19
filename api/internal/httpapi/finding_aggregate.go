package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	internalaudit "github.com/abchw517/kube-aiops/api/internal/audit"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const (
	allFindingClusters      = "*"
	unassignedFindingGroup  = "未分组"
	findingAggregateTimeout = 9 * time.Second
	findingAggregateWorkers = 8
)

type findingSource struct {
	id      string
	backend Backend
}

// findingSources 只返回在筛选范围内、同时可发现和可查询的诊断来源。
func (s *Server) findingSources(w http.ResponseWriter, r *http.Request, filter finding.Filter, capability authorization.Capability) ([]findingSource, bool) {
	clusters, ok := s.visibleClusters(w, r)
	if !ok {
		return nil, false
	}
	params := r.URL.Query()
	environment := strings.TrimSpace(params.Get("environment"))
	business := strings.TrimSpace(params.Get("business"))
	group := strings.TrimSpace(params.Get("group"))
	registry, multi := s.backend.(*ClusterBackends)
	principal, _ := identity.PrincipalFromContext(r.Context())
	anyAuthorized := false
	seen := make(map[string]bool)
	sources := make([]findingSource, 0, len(clusters))
	for _, cluster := range clusters {
		if seen[cluster.ID] {
			continue
		}
		seen[cluster.ID] = true
		if s.authorizationEnabled {
			scope := authorization.ClusterScope(cluster.ID)
			if filter.Namespace != "" {
				scope = authorization.NamespaceScope(cluster.ID, filter.Namespace)
			}
			decision, err := s.authorizer.Authorize(r.Context(), authorization.DecisionRequest{
				Principal: principal, Capability: capability, Scope: scope, Collection: true,
			})
			if err != nil {
				setAuditOutcome(r, internalaudit.OutcomeSecurityUnavailable)
				s.logAuthorizationUnavailable(r, capability, scope, "authorizer_error")
				writeAuthorizationUnavailable(w)
				return nil, false
			}
			if !decision.Allowed {
				continue
			}
		}
		anyAuthorized = true
		if !matchesFindingGroup(cluster.Environment, environment) ||
			!matchesFindingGroup(cluster.Business, business) ||
			!matchesFindingGroup(cluster.Group, group) {
			continue
		}
		backend := s.backend
		if multi {
			var target ClusterTarget
			var found bool
			if cluster.ID == localClusterID {
				target, found = registry.Local()
			} else {
				target, found = registry.Lookup(cluster.ID)
			}
			if !found || target.Backend == nil {
				writeError(w, http.StatusBadGateway, "FINDING_SOURCE_UNAVAILABLE", "finding source is no longer available")
				return nil, false
			}
			backend = target.Backend
		}
		sources = append(sources, findingSource{id: cluster.ID, backend: backend})
	}
	if s.authorizationEnabled && !anyAuthorized {
		setAuditOutcome(r, internalaudit.OutcomeDenied)
		writeForbidden(w)
		return nil, false
	}
	return sources, true
}

// matchesFindingGroup 与前端保持空维度的显示值一致。
func matchesFindingGroup(value, selected string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		value = unassignedFindingGroup
	}
	return selected == "" || selected == value
}

// aggregateFindingPage 合并每个来源在全局游标之后的一页。
func (s *Server) aggregateFindingPage(w http.ResponseWriter, r *http.Request, query finding.Query) (finding.Page, error, bool) {
	limit, err := finding.NormalizeLimit(query.Limit)
	if err != nil {
		return finding.Page{}, err, true
	}
	if _, err := finding.DecodeCursor(query.Continue); err != nil {
		return finding.Page{}, err, true
	}
	ctx, cancel := context.WithTimeout(r.Context(), findingAggregateTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	sources, ok := s.findingSources(w, r, query.Filter, authorization.CapabilityFindingsList)
	if !ok {
		return finding.Page{}, nil, false
	}
	pages, err := loadFindingSources(ctx, sources, func(ctx context.Context, source findingSource) (finding.Page, error) {
		q := query
		q.Limit = limit
		q.Cluster = source.id
		s.scopeFindingResults(r.WithContext(ctx), &q.Filter, authorization.CapabilityFindingsList)
		return source.backend.ListFindings(ctx, q)
	})
	if err != nil {
		return finding.Page{}, err, true
	}
	var items []finding.Finding
	moreAtSource := false
	for _, page := range pages {
		items = append(items, page.Items...)
		moreAtSource = moreAtSource || page.Pagination.Continue != ""
	}
	kubernetes.SortFindings(items)
	page, err := kubernetes.PageFindings(items, finding.Query{Limit: limit})
	if err == nil && moreAtSource && page.Pagination.Continue == "" && len(page.Items) > 0 {
		page.Pagination.Continue = finding.EncodeCursor(page.Items[len(page.Items)-1])
	}
	return page, err, true
}

// aggregateFindingSummary 与聚合列表使用同一范围，并合并来源的全部统计。
func (s *Server) aggregateFindingSummary(w http.ResponseWriter, r *http.Request, filter finding.Filter) (finding.Summary, error, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), findingAggregateTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	sources, ok := s.findingSources(w, r, filter, authorization.CapabilityFindingsSummary)
	if !ok {
		return finding.Summary{}, nil, false
	}
	summaries, err := loadFindingSources(ctx, sources, func(ctx context.Context, source findingSource) (finding.Summary, error) {
		f := filter
		f.Cluster = source.id
		s.scopeFindingResults(r.WithContext(ctx), &f, authorization.CapabilityFindingsSummary)
		return source.backend.SummarizeFindings(ctx, f)
	})
	if err != nil {
		return finding.Summary{}, err, true
	}
	summary := kubernetes.SummarizeFindings(nil)
	for _, item := range summaries {
		summary.Total += item.Total
		mergeFindingCounts(summary.BySeverity, item.BySeverity)
		mergeFindingCounts(summary.ByKind, item.ByKind)
		mergeFindingCounts(summary.ByNamespace, item.ByNamespace)
	}
	return summary, nil, true
}

// mergeFindingCounts 按既有汇总维度累加各实例计数。
func mergeFindingCounts(total, source map[string]int) {
	for key, count := range source {
		total[key] += count
	}
}

// loadFindingSources 有界并行查询，任一实例失败时取消剩余读取。
func loadFindingSources[T any](ctx context.Context, sources []findingSource, load func(context.Context, findingSource) (T, error)) ([]T, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan findingSource, len(sources))
	for _, source := range sources {
		jobs <- source
	}
	close(jobs)
	type result struct {
		value T
		err   error
	}
	results := make(chan result)
	for range min(findingAggregateWorkers, len(sources)) {
		go func() {
			for source := range jobs {
				if ctx.Err() != nil {
					return
				}
				value, err := load(ctx, source)
				if err != nil {
					err = fmt.Errorf("finding source %s: %w", source.id, err)
				}
				select {
				case results <- result{value: value, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	out := make([]T, 0, len(sources))
	for range sources {
		select {
		case entry := <-results:
			if entry.err != nil {
				return nil, entry.err
			}
			out = append(out, entry.value)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return out, nil
}
