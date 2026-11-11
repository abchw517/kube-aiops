package victorialogs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/observability"
)

const (
	bucketStep       = time.Minute
	maxResponseBytes = 64 << 10
)

var errIncomplete = errors.New("VictoriaLogs evidence incomplete")

type logClass struct {
	name     string
	pattern  string
	severity string
}

// v1 of the fixed classification catalog. A log can contribute to several classes.
var catalog = []logClass{
	{"panic_fatal", `(?i)\b(panic|fatal)\b`, finding.SeverityCritical},
	{"out_of_memory", `(?i)\b(oomkilled|oomkill|outofmemoryerror|out of memory)\b`, finding.SeverityCritical},
	{"connection_timeout", `(?i)\b(connection timed out|connection timeout|i/o timeout|connect timeout)\b`, finding.SeverityWarning},
	{"upstream_5xx", `(?i)\b(upstream( status)?|http(/[0-9.]+)?)[[:space:]]+5[0-9]{2}\b`, finding.SeverityWarning},
	{"jvm_gc_pressure", `(?i)\b(gc overhead limit exceeded|full gc|pause full)\b`, finding.SeverityWarning},
	{"application_restart", `(?i)\b(application restart(ed|ing)?|restarting application|restarting process)\b`, finding.SeverityInfo},
}

type Source struct {
	client       *observability.Client
	clusterLabel string
}

func New(config observability.Config) (correlation.LogSignalSource, error) {
	client, err := observability.NewClient("VICTORIALOGS", config)
	if err != nil || client == nil {
		return nil, err
	}
	return &Source{client: client, clusterLabel: config.ClusterLabelValue}, nil
}

func (s *Source) LogSignals(ctx context.Context, query correlation.Query) ([]correlation.CorrelationSignal, error) {
	resource := query.Scope.Resource
	if resource.Kind != "Pod" {
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

	selector := fmt.Sprintf(`{"kubernetes.pod_namespace"=%s,"kubernetes.pod_name"=%s}`,
		strconv.Quote(resource.Namespace), strconv.Quote(resource.Name))
	if s.clusterLabel != "" {
		selector += " cluster:=" + strconv.Quote(s.clusterLabel)
	}
	aggregates := make([]string, 0, len(catalog))
	for _, class := range catalog {
		aggregates = append(aggregates, "count() if (_msg:~"+strconv.Quote(class.pattern)+") as "+class.name)
	}
	params := url.Values{
		"query":   {selector + " | stats " + strings.Join(aggregates, ", ")},
		"start":   {start.UTC().Format(time.RFC3339Nano)},
		"end":     {end.UTC().Format(time.RFC3339Nano)},
		"step":    {"1m"},
		"timeout": {fmt.Sprintf("%dms", max(1, timeout.Milliseconds()))},
	}

	body, err := s.client.Get(ctx, "select/logsql/stats_query_range", params, token, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	return decodeSignals(body, resource, start, end, query.Budget.MaxSignalsPerSource)
}

type logSeries struct {
	Metric struct {
		Name string `json:"__name__"`
	} `json:"metric"`
	Values [][]json.RawMessage `json:"values"`
}

func decodeSignals(body []byte, resource finding.ResourceRef, start, end time.Time, limit int) ([]correlation.CorrelationSignal, error) {
	var result struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string      `json:"resultType"`
			Result     []logSeries `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Status != "success" || result.Data.ResultType != "matrix" ||
		result.Data.Result == nil || len(result.Data.Result) > len(catalog) {
		return nil, errIncomplete
	}

	var sourceErr error
	if len(result.Warnings) > 0 {
		sourceErr = errIncomplete
	}
	seriesByName := make(map[string]logSeries, len(result.Data.Result))
	for _, series := range result.Data.Result {
		if _, exists := seriesByName[series.Metric.Name]; exists {
			return nil, errIncomplete
		}
		seriesByName[series.Metric.Name] = series
	}

	signals := make([]correlation.CorrelationSignal, 0, len(seriesByName))
	for _, class := range catalog {
		series, exists := seriesByName[class.name]
		if !exists {
			continue
		}
		delete(seriesByName, class.name)
		signal, err := normalizeSeries(series, class, resource, start, end)
		if err != nil {
			sourceErr = errIncomplete
		} else if signal.Count > 0 {
			signals = append(signals, signal)
		}
	}
	if len(seriesByName) > 0 {
		sourceErr = errIncomplete
	}
	if len(signals) > limit {
		return signals[:limit], errIncomplete
	}
	return signals, sourceErr
}

func normalizeSeries(series logSeries, class logClass, resource finding.ResourceRef, start, end time.Time) (correlation.CorrelationSignal, error) {
	bucketStart := start.Truncate(bucketStep)
	maxBuckets := int(end.Add(-time.Nanosecond).Truncate(bucketStep).Sub(bucketStart)/bucketStep) + 1
	if len(series.Values) == 0 || len(series.Values) > maxBuckets {
		return correlation.CorrelationSignal{}, errIncomplete
	}
	var first, last, previous time.Time
	var count int
	for _, sample := range series.Values {
		if len(sample) != 2 {
			return correlation.CorrelationSignal{}, errIncomplete
		}
		var seconds *int64
		var text string
		if json.Unmarshal(sample[0], &seconds) != nil || seconds == nil || json.Unmarshal(sample[1], &text) != nil {
			return correlation.CorrelationSignal{}, errIncomplete
		}
		stamp := time.Unix(*seconds, 0)
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(value) || value < 0 || value != math.Trunc(value) ||
			value > float64(math.MaxInt32-count) || stamp.Before(bucketStart) || !stamp.Before(end) ||
			!stamp.Equal(stamp.Truncate(bucketStep)) || (!previous.IsZero() && !stamp.After(previous)) {
			return correlation.CorrelationSignal{}, errIncomplete
		}
		previous = stamp
		if value == 0 {
			continue
		}
		stamp = stamp.UTC()
		if stamp.Before(start) {
			stamp = start.UTC()
		}
		if count == 0 {
			first = stamp
		}
		last = stamp
		count += int(value)
	}
	if count == 0 {
		return correlation.CorrelationSignal{}, nil
	}
	id := "victorialogs/v1/" + class.name
	digest := sha256.Sum256([]byte(id))
	return correlation.CorrelationSignal{
		ID: id, Source: correlation.SourceVictoriaLogs, Type: correlation.SignalTypeLog,
		Name: class.name, Severity: class.severity, Resource: &resource, Count: count,
		FirstSeen: first.Format(time.RFC3339Nano), LastSeen: last.Format(time.RFC3339Nano),
		Fingerprint: fmt.Sprintf("sha256:%x", digest),
	}, nil
}
