package kubernetes

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/k8sgpt"
)

const (
	resultPageSize = 200
	maxResultScan  = 5000
)

func (c *Client) ListFindings(ctx context.Context, query finding.Query) (finding.Page, error) {
	limit, err := finding.NormalizeLimit(query.Limit)
	if err != nil {
		return finding.Page{}, err
	}
	cursor, err := finding.DecodeCursor(query.Continue)
	if err != nil {
		return finding.Page{}, err
	}
	items, err := c.filteredFindings(ctx, query.Filter)
	if err != nil {
		return finding.Page{}, err
	}

	start := cursorStart(items, cursor)
	end := start + limit
	if end > len(items) {
		end = len(items)
	}

	// Always allocate the collection, including the zero-length case, so the
	// public JSON contract is stable (`items: []`) instead of switching to
	// `items: null` when a filter has no matches.
	pageItems := make([]finding.Finding, end-start)
	copy(pageItems, items[start:end])

	next := ""
	if end < len(items) && len(pageItems) > 0 {
		next = finding.EncodeCursor(pageItems[len(pageItems)-1])
	}

	return finding.Page{
		Items: pageItems,
		Pagination: finding.Pagination{
			Limit:    limit,
			Continue: next,
		},
	}, nil
}

func (c *Client) GetFinding(ctx context.Context, id string) (finding.Finding, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return finding.Finding{}, &APIError{StatusCode: http.StatusNotFound}
	}

	var result k8sgpt.Result
	endpoint := "/apis/core.k8sgpt.ai/v1alpha1/namespaces/" +
		url.PathEscape(c.config.K8sGPTNS) + "/results/" + url.PathEscape(id)
	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return finding.Finding{}, err
	}
	if !c.belongsToCurrentK8sGPT(result.Metadata.Labels) {
		return finding.Finding{}, &APIError{StatusCode: http.StatusNotFound}
	}
	return finding.FromResult(result), nil
}

func (c *Client) SummarizeFindings(ctx context.Context, filter finding.Filter) (finding.Summary, error) {
	items, err := c.filteredFindings(ctx, filter)
	if err != nil {
		return finding.Summary{}, err
	}

	summary := finding.Summary{
		Total: len(items),
		BySeverity: map[string]int{
			finding.SeverityCritical: 0,
			finding.SeverityWarning:  0,
			finding.SeverityInfo:     0,
		},
		ByKind:      map[string]int{},
		ByNamespace: map[string]int{},
	}
	for _, item := range items {
		summary.BySeverity[item.Severity]++
		if item.Resource.Kind != "" {
			summary.ByKind[item.Resource.Kind]++
		}
		if item.Namespace != "" {
			summary.ByNamespace[item.Namespace]++
		}
	}
	return summary, nil
}

func (c *Client) filteredFindings(ctx context.Context, filter finding.Filter) ([]finding.Finding, error) {
	results, err := c.listCurrentResults(ctx)
	if err != nil {
		return nil, err
	}

	items := make([]finding.Finding, 0, len(results))
	for _, result := range results {
		item := finding.FromResult(result)
		if finding.Matches(item, filter) {
			items = append(items, item)
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt > items[j].CreatedAt
	})
	return items, nil
}

func (c *Client) listCurrentResults(ctx context.Context) ([]k8sgpt.Result, error) {
	items := make([]k8sgpt.Result, 0)
	continueToken := ""

	for {
		query := url.Values{}
		query.Set("limit", strconv.Itoa(resultPageSize))
		query.Set("labelSelector", fmt.Sprintf(
			"k8sgpts.k8sgpt.ai/name=%s,k8sgpts.k8sgpt.ai/namespace=%s",
			c.config.K8sGPTName,
			c.config.K8sGPTNS,
		))
		if continueToken != "" {
			query.Set("continue", continueToken)
		}

		var page k8sgpt.ResultList
		endpoint := "/apis/core.k8sgpt.ai/v1alpha1/namespaces/" +
			url.PathEscape(c.config.K8sGPTNS) + "/results?" + query.Encode()
		if err := c.getJSON(ctx, endpoint, &page); err != nil {
			return nil, err
		}

		items = append(items, page.Items...)
		if len(items) > maxResultScan {
			return nil, finding.ErrTooMany
		}
		continueToken = page.Metadata.Continue
		if continueToken == "" {
			break
		}
	}

	return items, nil
}

func (c *Client) belongsToCurrentK8sGPT(labels map[string]string) bool {
	return labels["k8sgpts.k8sgpt.ai/name"] == c.config.K8sGPTName &&
		labels["k8sgpts.k8sgpt.ai/namespace"] == c.config.K8sGPTNS
}

func cursorStart(items []finding.Finding, cursor *finding.Cursor) int {
	if cursor == nil {
		return 0
	}
	for i, item := range items {
		if item.CreatedAt < cursor.CreatedAt ||
			(item.CreatedAt == cursor.CreatedAt && item.ID > cursor.ID) {
			return i
		}
	}
	return len(items)
}
