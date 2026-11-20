package wiki

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type Target struct {
	Cluster     string   `json:"cluster"`
	Namespace   string   `json:"namespace"`
	Environment string   `json:"environment,omitempty"`
	Businesses  []string `json:"businesses,omitempty"`
}

type TargetResolver func(context.Context, string, string) (Target, error)

type targetCacheKey struct{}
type resolvedTarget struct {
	target Target
	err    error
}

func withTargetCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, targetCacheKey{}, make(map[[2]string]resolvedTarget))
}

func (a *Service) currentTarget(ctx context.Context, cluster, namespace string) (Target, error) {
	if a.resolve == nil {
		return Target{}, nil
	}
	cache, _ := ctx.Value(targetCacheKey{}).(map[[2]string]resolvedTarget)
	key := [2]string{cluster, namespace}
	if result, ok := cache[key]; ok {
		return result.target, result.err
	}
	target, err := a.resolve(ctx, cluster, namespace)
	slices.Sort(target.Businesses)
	target.Businesses = slices.Compact(target.Businesses)
	if cache != nil {
		cache[key] = resolvedTarget{target, err}
	}
	return target, err
}

type Metadata struct {
	Targets       []Target          `json:"targets,omitempty"`
	Team          string            `json:"team,omitempty"`
	Applicability string            `json:"applicability,omitempty"`
	ResourceType  string            `json:"resourceType,omitempty"`
	Signals       []string          `json:"signals,omitempty"`
	Sections      map[string]string `json:"sections,omitempty"`
	ReviewedAt    string            `json:"reviewedAt,omitempty"`
	ReviewBy      string            `json:"reviewBy,omitempty"`
	SourceID      string            `json:"sourceId,omitempty"`
	Source        string            `json:"source,omitempty"`
	Finding       *FindingSource    `json:"finding,omitempty"`
}

type FindingSource struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
	Revision  int    `json:"revision"`
}

const publishedQuery = `SELECT d.id,d.cluster,d.namespace,d.environment,d.business,d.document_type,d.title,d.body,d.created_at,
	COALESCE((SELECT MAX(revision) FROM wiki_revisions WHERE knowledge_id=d.id),1),
	COALESCE(p.metadata,'{}'),COALESCE(p.withdrawn,0) OR EXISTS(
	SELECT 1 FROM wiki_remote_pages remote JOIN wiki_disabled_sources disabled ON disabled.id=remote.source_id WHERE remote.knowledge_id=d.id)
	FROM wiki_documents d LEFT JOIN wiki_publications p ON p.knowledge_id=d.id`

type scanner interface{ Scan(...any) error }

func readPublished(row scanner, doc *Document) error {
	var metadata string
	if err := row.Scan(&doc.ID, &doc.Cluster, &doc.Namespace, &doc.Environment, &doc.Business, &doc.DocumentType, &doc.Title, &doc.Body, &doc.CreatedAt,
		&doc.Revision, &metadata, &doc.Withdrawn); err != nil {
		return err
	}
	return json.Unmarshal([]byte(metadata), &doc.Metadata)
}

func documentTargets(doc Document) []Target {
	if len(doc.Metadata.Targets) != 0 {
		return doc.Metadata.Targets
	}
	return []Target{{Cluster: doc.Cluster, Namespace: doc.Namespace, Environment: doc.Environment, Businesses: []string{doc.Business}}}
}

func (a *Service) allowedTarget(ctx context.Context, principal identity.Principal, capability authorization.Capability, target Target) (bool, error) {
	scope := authorization.NamespaceScope(target.Cluster, target.Namespace)
	if scope.Validate() != nil || target.Namespace == "" || target.Cluster == "*" || target.Namespace == "*" {
		return false, nil
	}
	return a.permitTarget(ctx, principal, capability, scope)
}

func (a *Service) readableTarget(ctx context.Context, p identity.Principal, target Target, verified bool) (bool, error) {
	ok, err := a.allowedTarget(ctx, p, authorization.CapabilityWikiRead, target)
	if !ok || err != nil {
		return ok, err
	}
	if operation, _ := ctx.Value(wikiOperationKey{}).(wikiOperation); operation.key == catalog.WikiAsk {
		ok, err = a.permit(ctx, p, catalog.WikiAsk, authorization.CapabilityWikiRead, authorization.NamespaceScope(target.Cluster, target.Namespace))
		if !ok || err != nil {
			return ok, err
		}
	}
	if !verified {
		return true, nil
	}
	current, err := a.currentTarget(ctx, target.Cluster, target.Namespace)
	return targetsEqual(current, target), err
}

func (a *Service) unresolvedLegacyScope(ctx context.Context, doc Document) (bool, error) {
	if a.resolve == nil || len(doc.Metadata.Targets) > 0 {
		return false, nil
	}
	current, err := a.currentTarget(ctx, doc.Cluster, doc.Namespace)
	return current.Environment == "", err
}

func (a *Service) canRead(ctx context.Context, principal identity.Principal, doc Document, target *Target, recommend bool) (bool, error) {
	if doc.Withdrawn || recommend && (doc.Metadata.ReviewedAt == "" ||
		doc.DocumentType == "操作规程" && (doc.Environment == "production" || slices.ContainsFunc(doc.Metadata.Targets, func(t Target) bool { return t.Environment == "production" })) &&
			doc.Metadata.ReviewBy < time.Now().Format(time.DateOnly)) {
		return false, nil
	}
	for _, entry := range documentTargets(doc) {
		if target != nil && (entry.Cluster != target.Cluster || entry.Namespace != target.Namespace ||
			target.Environment != "" && !targetsEqual(entry, *target)) {
			continue
		}
		ok, err := a.readableTarget(ctx, principal, entry, len(doc.Metadata.Targets) > 0)
		if err != nil {
			return false, err
		}
		if ok {
			pending, err := a.unresolvedLegacyScope(ctx, doc)
			return !pending, err
		}
	}
	return false, nil
}

func (a *Service) projectTargets(ctx context.Context, principal identity.Principal, doc *Document) {
	if len(doc.Metadata.Targets) > 0 {
		var visible []Target
		for _, target := range doc.Metadata.Targets {
			if ok, _ := a.readableTarget(ctx, principal, target, true); ok {
				visible = append(visible, target)
			}
		}
		doc.Metadata.Targets = visible
		if len(visible) > 0 {
			doc.Cluster, doc.Namespace, doc.Environment = visible[0].Cluster, visible[0].Namespace, visible[0].Environment
			doc.Business = strings.Join(visible[0].Businesses, ",")
		}
	}
	if src := doc.Metadata.Finding; src != nil {
		allowed, err := a.findingAllowed(ctx, principal, *src)
		if err != nil || !allowed {
			doc.Metadata.Finding = nil
		}
	}
}

func (a *Service) findingAllowed(ctx context.Context, principal identity.Principal, src FindingSource) (bool, error) {
	if src.Namespace == "" {
		return permissions.IsAdministrator(principal), nil
	}
	return a.allowedTarget(ctx, principal, authorization.CapabilityFindingsRead, Target{Cluster: src.Cluster, Namespace: src.Namespace})
}

func (a *Service) searchDocuments(ctx context.Context, p identity.Principal, query url.Values, recommend bool, target *Target) ([]Document, map[string][]string, error) {
	if ctx.Value(targetCacheKey{}) == nil {
		ctx = withTargetCache(ctx)
	}
	rows, err := a.store.QueryContext(ctx, publishedQuery)
	if err != nil {
		return nil, nil, err
	}
	var docs []Document
	for rows.Next() {
		var d Document
		if err = readPublished(rows, &d); err != nil {
			rows.Close()
			return nil, nil, err
		}
		docs = append(docs, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	type match struct {
		doc   Document
		score int
	}
	var matches []match
	filters := map[string][]string{"environment": {}, "business": {}, "resourceType": {}, "documentType": {}}
	q := strings.ToLower(strings.TrimSpace(query.Get("q")))
	for _, doc := range docs {
		allowed, err := a.canRead(ctx, p, doc, target, recommend)
		if err != nil {
			return nil, nil, err
		}
		if !allowed {
			continue
		}
		a.projectTargets(ctx, p, &doc)
		targets := documentTargets(doc)
		for _, entry := range targets {
			filters["environment"] = addFilter(filters["environment"], entry.Environment)
			for _, business := range entry.Businesses {
				filters["business"] = addFilter(filters["business"], business)
			}
		}
		filters["resourceType"] = addFilter(filters["resourceType"], doc.Metadata.ResourceType)
		filters["documentType"] = addFilter(filters["documentType"], doc.DocumentType)
		if !slices.ContainsFunc(targets, func(entry Target) bool {
			return (query.Get("environment") == "" || query.Get("environment") == entry.Environment) &&
				(query.Get("business") == "" || slices.Contains(entry.Businesses, query.Get("business")))
		}) || query.Get("documentType") != "" && query.Get("documentType") != doc.DocumentType ||
			query.Get("resourceType") != "" && query.Get("resourceType") != doc.Metadata.ResourceType {
			continue
		}
		score, reason := searchScore(q, doc, recommend && target != nil)
		if q != "" && score == 0 {
			continue
		}
		doc.Match = reason
		matches = append(matches, match{doc, score})
	}
	for _, values := range filters {
		slices.Sort(values)
	}
	slices.SortFunc(matches, func(a, b match) int {
		if a.score != b.score {
			return b.score - a.score
		}
		if overdue(a.doc) != overdue(b.doc) {
			if overdue(a.doc) {
				return 1
			}
			return -1
		}
		if date := strings.Compare(b.doc.CreatedAt, a.doc.CreatedAt); date != 0 {
			return date
		}
		return strings.Compare(a.doc.ID, b.doc.ID)
	})
	results := make([]Document, 0, len(matches))
	for _, entry := range matches {
		results = append(results, entry.doc)
	}
	return results, filters, nil
}

func overdue(doc Document) bool {
	return doc.Metadata.ReviewBy == "" || doc.Metadata.ReviewBy < time.Now().Format(time.DateOnly)
}

func addFilter(items []string, value string) []string {
	if value != "" && !slices.Contains(items, value) {
		return append(items, value)
	}
	return items
}

func searchScore(q string, doc Document, recommend bool) (int, string) {
	if q == "" {
		return 0, ""
	}
	for _, signal := range doc.Metadata.Signals {
		token := strings.ToLower(signal)
		if token != "" && (q == token || strings.Contains(q, token)) {
			return 100, "命中 " + signal
		}
	}
	if recommend {
		return 0, ""
	}
	if strings.Contains(strings.ToLower(doc.Title), q) {
		return 50, "标题匹配"
	}
	text := strings.ToLower(doc.Body + " " + doc.Metadata.Applicability + " " + doc.Metadata.ResourceType + " " + strings.Join(doc.Metadata.Signals, " "))
	if strings.Contains(text, q) {
		return 30, "正文或适用条件匹配"
	}
	var count int
	for _, term := range wikiSearchTerms(q) {
		if strings.Contains(text, term) {
			count++
		}
	}
	if count > 0 {
		return min(count, 20), "关键词匹配"
	}
	return 0, ""
}
