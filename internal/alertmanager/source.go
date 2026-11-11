package alertmanager

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/observability"
)

const (
	maxCandidates    = 200
	maxResponseBytes = 256 << 10
)

var (
	errIncomplete = errors.New("Alertmanager evidence incomplete")
	alertName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]{0,127}$`)
	fingerprint   = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

var resourceLabels = map[string]string{
	"Pod": "pod", "Deployment": "deployment", "StatefulSet": "statefulset",
	"DaemonSet": "daemonset", "ReplicaSet": "replicaset", "Job": "job_name",
	"CronJob": "cronjob", "PersistentVolumeClaim": "persistentvolumeclaim",
	"Node": "node",
}

type Source struct {
	client       *observability.Client
	clusterLabel string
}

func New(config observability.Config) (correlation.AlertSource, error) {
	client, err := observability.NewClient("ALERTMANAGER", config)
	if err != nil || client == nil {
		return nil, err
	}
	return &Source{client: client, clusterLabel: config.ClusterLabelValue}, nil
}

type matcher struct {
	name  string
	value string
}

func (s *Source) Alerts(ctx context.Context, query correlation.Query) ([]correlation.CorrelationSignal, error) {
	resource := query.Scope.Resource
	label, supported := resourceLabels[resource.Kind]
	if !supported {
		return nil, nil
	}
	if err := observability.ValidateScope(query.Scope); err != nil {
		return nil, err
	}
	start, end, timeout, err := observability.QueryBounds(query)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	token, err := s.client.BearerToken()
	if err != nil {
		return nil, err
	}

	matchers := []matcher{{label, resource.Name}}
	if resource.Namespace != "" {
		matchers = append(matchers, matcher{"namespace", resource.Namespace})
	}
	if s.clusterLabel != "" {
		matchers = append(matchers, matcher{"cluster", s.clusterLabel})
	}
	params := url.Values{
		"active": {"true"}, "silenced": {"true"}, "inhibited": {"true"}, "unprocessed": {"true"},
	}
	for _, filter := range matchers {
		params.Add("filter", filter.name+"="+strconv.Quote(filter.value))
	}
	body, err := s.client.Get(ctx, "api/v2/alerts", params, token, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	return decodeAlerts(body, resource, matchers, start, end, query.Budget.MaxSignalsPerSource)
}

type upstreamAlert struct {
	Labels      map[string]string `json:"labels"`
	Fingerprint string            `json:"fingerprint"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt"`
	Status      struct {
		State string `json:"state"`
	} `json:"status"`
}

func decodeAlerts(body []byte, resource finding.ResourceRef, matchers []matcher, start, end time.Time, limit int) ([]correlation.CorrelationSignal, error) {
	var alerts []upstreamAlert
	if err := json.Unmarshal(body, &alerts); err != nil || alerts == nil {
		return nil, errIncomplete
	}

	var sourceErr error
	if len(alerts) > maxCandidates {
		alerts = alerts[:maxCandidates]
		sourceErr = errIncomplete
	}
	signals := make([]correlation.CorrelationSignal, 0, len(alerts))
	seen := make(map[string]struct{}, len(alerts))
	for _, alert := range alerts {
		if !matchesResource(alert.Labels, matchers) {
			continue
		}
		signal, err := normalizeAlert(alert, resource, start, end)
		if err != nil {
			sourceErr = errIncomplete
		} else if signal.ID != "" {
			if _, duplicate := seen[signal.ID]; !duplicate {
				seen[signal.ID] = struct{}{}
				signals = append(signals, signal)
			}
		}
	}
	slices.SortFunc(signals, func(a, b correlation.CorrelationSignal) int {
		// Use parsed timestamps to order fractional and whole seconds consistently.
		aTime, _ := time.Parse(time.RFC3339Nano, a.LastSeen)
		bTime, _ := time.Parse(time.RFC3339Nano, b.LastSeen)
		if comparison := bTime.Compare(aTime); comparison != 0 {
			return comparison
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if len(signals) > limit {
		return signals[:limit], errIncomplete
	}
	return signals, sourceErr
}

func matchesResource(labels map[string]string, matchers []matcher) bool {
	for _, expected := range matchers {
		if labels[expected.name] != expected.value {
			return false
		}
	}
	return true
}

func normalizeAlert(alert upstreamAlert, resource finding.ResourceRef, start, end time.Time) (correlation.CorrelationSignal, error) {
	first, firstErr := time.Parse(time.RFC3339Nano, alert.StartsAt)
	last, lastErr := time.Parse(time.RFC3339Nano, alert.EndsAt)
	if firstErr != nil || lastErr != nil || first.IsZero() || last.IsZero() || last.Before(first) {
		return correlation.CorrelationSignal{}, errIncomplete
	}
	if last.Before(start) || first.After(end) {
		return correlation.CorrelationSignal{}, nil
	}

	name := alert.Labels["alertname"]
	state := alert.Status.State
	if !alertName.MatchString(name) || !fingerprint.MatchString(alert.Fingerprint) ||
		(state != "active" && state != "suppressed" && state != "unprocessed") {
		return correlation.CorrelationSignal{}, errIncomplete
	}
	severity := alert.Labels["severity"]
	switch severity {
	case finding.SeverityCritical, finding.SeverityWarning, finding.SeverityInfo:
	default:
		severity = ""
	}
	if first.Before(start) {
		first = start
	}
	if last.After(end) {
		last = end
	}
	return correlation.CorrelationSignal{
		ID: "alertmanager/" + alert.Fingerprint, Source: correlation.SourceAlertmanager, Type: correlation.SignalTypeAlert,
		Name: name, Severity: severity, Resource: &resource, Count: 1, Fingerprint: alert.Fingerprint,
		FirstSeen: first.UTC().Format(time.RFC3339Nano), LastSeen: last.UTC().Format(time.RFC3339Nano),
		Summary: "Alertmanager state " + state,
	}, nil
}
