package kubernetes

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

const (
	maxEventCandidateCount = 200
	maxEventSummaryBytes   = 2048
)

type eventList struct {
	Items []eventRecord `json:"items"`
}

type eventRecord struct {
	Metadata struct {
		Name              string `json:"name"`
		Namespace         string `json:"namespace"`
		UID               string `json:"uid"`
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	InvolvedObject struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Namespace  string `json:"namespace"`
		Name       string `json:"name"`
	} `json:"involvedObject"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	Type               string `json:"type"`
	FirstTimestamp     string `json:"firstTimestamp"`
	LastTimestamp      string `json:"lastTimestamp"`
	EventTime          string `json:"eventTime"`
	ReportingComponent string `json:"reportingComponent"`
	Source             struct {
		Component string `json:"component"`
	} `json:"source"`
	Series *struct {
		Count            int    `json:"count"`
		LastObservedTime string `json:"lastObservedTime"`
	} `json:"series"`
	Count int `json:"count"`
}

// Events implements correlation.EventSource using only the Kubernetes core/v1 Events list API.
// The selector, time window and result limits are derived from the backend-owned correlation Query;
// callers cannot provide arbitrary Kubernetes selectors or proxy paths.
func (c *Client) Events(ctx context.Context, query correlation.Query) ([]correlation.CorrelationSignal, error) {
	endpoint, start, end, limit, err := eventListRequest(query)
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		return []correlation.CorrelationSignal{}, nil
	}

	var payload eventList
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		return nil, err
	}

	signals := make([]correlation.CorrelationSignal, 0, min(len(payload.Items), query.Budget.MaxSignalsPerSource))
	for _, item := range payload.Items {
		signal, ok := normalizeEventSignal(item, query, start, end)
		if !ok {
			continue
		}
		signals = append(signals, signal)
	}

	sort.SliceStable(signals, func(i, j int) bool {
		if signals[i].LastSeen == signals[j].LastSeen {
			return signals[i].ID < signals[j].ID
		}
		return signals[i].LastSeen > signals[j].LastSeen
	})
	if len(signals) > limit {
		signals = signals[:limit]
	}
	return signals, nil
}

func eventListRequest(query correlation.Query) (string, time.Time, time.Time, int, error) {
	if strings.TrimSpace(query.Scope.Cluster) != localClusterID {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("unsupported Kubernetes correlation cluster")
	}
	if query.Budget.MaxSignalsPerSource < 1 || query.Budget.MaxSignalsPerSource > correlation.MaxSignalsPerSource {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("invalid Kubernetes event signal budget")
	}
	start, err := time.Parse(time.RFC3339, query.Window.Start)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("invalid Kubernetes event window start")
	}
	end, err := time.Parse(time.RFC3339, query.Window.End)
	if err != nil || end.Before(start) || end.Sub(start) > correlation.MaxWindow {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("invalid Kubernetes event window end")
	}

	kind := strings.TrimSpace(query.Scope.Resource.Kind)
	name := strings.TrimSpace(query.Scope.Resource.Name)
	if kind == "" || name == "" {
		return "", start, end, query.Budget.MaxSignalsPerSource, nil
	}
	if err := validateFieldSelectorValue("kind", kind); err != nil {
		return "", time.Time{}, time.Time{}, 0, err
	}
	if err := validateFieldSelectorValue("name", name); err != nil {
		return "", time.Time{}, time.Time{}, 0, err
	}

	namespace := strings.TrimSpace(query.Scope.Namespace)
	if resourceNamespace := strings.TrimSpace(query.Scope.Resource.Namespace); namespace != "" && resourceNamespace != "" && resourceNamespace != namespace {
		return "", time.Time{}, time.Time{}, 0, fmt.Errorf("Kubernetes event correlation namespace mismatch")
	}
	if namespace != "" {
		if err := validateFieldSelectorValue("namespace", namespace); err != nil {
			return "", time.Time{}, time.Time{}, 0, err
		}
	}

	candidateLimit := query.Budget.MaxSignalsPerSource * 4
	if candidateLimit > maxEventCandidateCount {
		candidateLimit = maxEventCandidateCount
	}
	if candidateLimit < query.Budget.MaxSignalsPerSource {
		candidateLimit = query.Budget.MaxSignalsPerSource
	}

	values := url.Values{}
	values.Set("fieldSelector", "involvedObject.kind="+kind+",involvedObject.name="+name)
	values.Set("limit", fmt.Sprintf("%d", candidateLimit))

	path := "/api/v1/events"
	if namespace != "" {
		path = "/api/v1/namespaces/" + url.PathEscape(namespace) + "/events"
	}
	return path + "?" + values.Encode(), start.UTC(), end.UTC(), query.Budget.MaxSignalsPerSource, nil
}

func validateFieldSelectorValue(field, value string) error {
	if len(value) > 253 || strings.ContainsAny(value, ",=\\\n\r\x00") {
		return fmt.Errorf("invalid Kubernetes event selector %s", field)
	}
	return nil
}

func normalizeEventSignal(item eventRecord, query correlation.Query, start, end time.Time) (correlation.CorrelationSignal, bool) {
	kind := strings.TrimSpace(item.InvolvedObject.Kind)
	name := strings.TrimSpace(item.InvolvedObject.Name)
	namespace := strings.TrimSpace(item.InvolvedObject.Namespace)
	if kind != strings.TrimSpace(query.Scope.Resource.Kind) || name != strings.TrimSpace(query.Scope.Resource.Name) {
		return correlation.CorrelationSignal{}, false
	}
	if query.Scope.Namespace != "" && namespace != query.Scope.Namespace {
		return correlation.CorrelationSignal{}, false
	}

	first := firstEventTime(item)
	last := lastEventTime(item)
	if first.IsZero() && last.IsZero() {
		return correlation.CorrelationSignal{}, false
	}
	if first.IsZero() {
		first = last
	}
	if last.IsZero() {
		last = first
	}
	if last.Before(start) || first.After(end) {
		return correlation.CorrelationSignal{}, false
	}
	if first.Before(start) {
		first = start
	}
	if last.After(end) {
		last = end
	}

	id := strings.TrimSpace(item.Metadata.UID)
	if id == "" {
		id = strings.TrimSpace(item.Metadata.Name)
	}
	if id == "" {
		return correlation.CorrelationSignal{}, false
	}

	count := item.Count
	if item.Series != nil && item.Series.Count > 0 {
		count = item.Series.Count
	}
	if count < 1 {
		count = 1
	}

	severity := finding.SeverityInfo
	if strings.EqualFold(strings.TrimSpace(item.Type), "Warning") {
		severity = finding.SeverityWarning
	}

	reason := strings.TrimSpace(item.Reason)
	message := strings.TrimSpace(item.Message)
	summary := message
	if reason != "" && message != "" {
		summary = reason + ": " + message
	} else if reason != "" {
		summary = reason
	}
	summary = truncateUTF8(summary, maxEventSummaryBytes)

	apiVersion := strings.TrimSpace(item.InvolvedObject.APIVersion)
	resource := finding.ResourceRef{
		APIVersion: apiVersion,
		Kind:       kind,
		Namespace:  namespace,
		Name:       name,
	}

	return correlation.CorrelationSignal{
		ID:        id,
		Source:    correlation.SourceKubernetesEvents,
		Type:      correlation.SignalTypeEvent,
		Severity:  severity,
		Resource:  &resource,
		FirstSeen: first.UTC().Format(time.RFC3339),
		LastSeen:  last.UTC().Format(time.RFC3339),
		Count:     count,
		Name:      reason,
		Summary:   summary,
		Evidence: []correlation.EvidenceRef{{
			Source: correlation.SourceKubernetesEvents,
			ID:     id,
		}},
	}, true
}

func firstEventTime(item eventRecord) time.Time {
	for _, value := range []string{item.FirstTimestamp, item.EventTime, item.Metadata.CreationTimestamp} {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func lastEventTime(item eventRecord) time.Time {
	values := make([]string, 0, 4)
	if item.Series != nil {
		values = append(values, item.Series.LastObservedTime)
	}
	values = append(values, item.LastTimestamp, item.EventTime, item.Metadata.CreationTimestamp)
	for _, value := range values {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && cut < len(value) && value[cut]&0xc0 == 0x80 {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}
