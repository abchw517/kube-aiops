package prometheus

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/observability"
)

const (
	rangeStep        = time.Minute
	maxResponseBytes = 64 << 10
)

var errQuery = errors.New("Prometheus metric evidence incomplete")

type Config = observability.Config

type Source struct {
	clusterLabel string
	client       *observability.Client
}

func New(config Config) (correlation.MetricSource, error) {
	client, err := observability.NewClient("PROMETHEUS", config)
	if err != nil || client == nil {
		return nil, err
	}
	return &Source{client: client, clusterLabel: config.ClusterLabelValue}, nil
}

func (s *Source) Metrics(ctx context.Context, query correlation.Query) ([]correlation.CorrelationSignal, error) {
	definitions := metricCatalog[query.Scope.Resource.Kind]
	if len(definitions) == 0 {
		return nil, nil
	}
	selector, err := s.selector(query.Scope)
	if err != nil {
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

	type metricResult struct {
		signal correlation.CorrelationSignal
		err    error
	}
	results := make([]metricResult, len(definitions))
	var queries sync.WaitGroup
	for i, metric := range definitions {
		queries.Go(func() {
			results[i].signal, results[i].err = s.query(ctx, metric, selector, token, start, end, timeout, query.Scope.Resource)
		})
	}
	queries.Wait()

	signals := make([]correlation.CorrelationSignal, 0, len(results))
	var sourceErr error
	for _, result := range results {
		if result.err != nil {
			sourceErr = errQuery
		}
		if result.signal.Count > 0 {
			signals = append(signals, result.signal)
		}
	}
	if len(signals) > query.Budget.MaxSignalsPerSource {
		return signals[:query.Budget.MaxSignalsPerSource], errQuery
	}
	return signals, sourceErr
}

func (s *Source) selector(scope correlation.CorrelationScope) (string, error) {
	if err := observability.ValidateScope(scope); err != nil {
		return "", err
	}
	resource := scope.Resource
	var matchers []string
	if resource.Kind == "Node" {
		matchers = append(matchers, "node="+strconv.Quote(resource.Name))
	} else {
		matchers = append(matchers, "namespace="+strconv.Quote(scope.Namespace),
			strings.ToLower(resource.Kind)+"="+strconv.Quote(resource.Name))
	}
	if s.clusterLabel != "" {
		matchers = append(matchers, "cluster="+strconv.Quote(s.clusterLabel))
	}
	return strings.Join(matchers, ","), nil
}

func (s *Source) query(ctx context.Context, metric metricDefinition, selector, token string, start, end time.Time, timeout time.Duration, resource finding.ResourceRef) (correlation.CorrelationSignal, error) {
	params := url.Values{
		"query":   {fmt.Sprintf(metric.expression, selector)},
		"limit":   {"1"},
		"timeout": {fmt.Sprintf("%dms", max(1, timeout.Milliseconds()))},
	}
	path := "api/v1/query_range"
	if metric.instant {
		path = "api/v1/query"
		params.Set("time", end.UTC().Format(time.RFC3339Nano))
	} else {
		params.Set("start", start.UTC().Format(time.RFC3339Nano))
		params.Set("end", end.UTC().Format(time.RFC3339Nano))
		params.Set("step", strconv.FormatInt(int64(rangeStep/time.Second), 10))
	}
	body, err := s.client.Get(ctx, path, params, token, maxResponseBytes)
	if err != nil {
		return correlation.CorrelationSignal{}, errQuery
	}
	return decodeSignal(body, metric, start, end, resource)
}
