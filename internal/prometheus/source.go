package prometheus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

const (
	rangeStep        = time.Minute
	maxResponseBytes = 64 << 10
)

var (
	objectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	errQuery   = errors.New("Prometheus metric evidence incomplete")
)

type Config struct {
	URL               string
	ClusterLabelValue string
	TokenFile         string
	CAFile            string
}

type Source struct {
	baseURL      *url.URL
	clusterLabel string
	tokenFile    string
	client       *http.Client
}

func New(config Config) (correlation.MetricSource, error) {
	if config.URL == "" {
		if config.ClusterLabelValue != "" || config.TokenFile != "" || config.CAFile != "" {
			return nil, errors.New("Prometheus settings require PROMETHEUS_URL")
		}
		return nil, nil
	}

	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.ForceQuery || strings.Contains(config.URL, "#") || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return nil, errors.New("PROMETHEUS_URL must be an HTTP(S) base URL")
	}
	if endpoint.Scheme != "https" && (config.TokenFile != "" || config.CAFile != "") {
		return nil, errors.New("Prometheus TLS settings require an HTTPS endpoint")
	}
	if len(config.ClusterLabelValue) > 253 || strings.IndexFunc(config.ClusterLabelValue, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid PROMETHEUS_CLUSTER_LABEL_VALUE")
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.CAFile != "" {
		ca, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, errors.New("read Prometheus CA file")
		}
		tlsConfig.RootCAs = x509.NewCertPool()
		if !tlsConfig.RootCAs.AppendCertsFromPEM(ca) {
			return nil, errors.New("parse Prometheus CA file")
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &Source{
		baseURL:      endpoint,
		clusterLabel: config.ClusterLabelValue,
		tokenFile:    config.TokenFile,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
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
	start, startErr := time.Parse(time.RFC3339, query.Window.Start)
	end, endErr := time.Parse(time.RFC3339, query.Window.End)
	window := end.Sub(start)
	if startErr != nil || endErr != nil || window <= 0 || window > correlation.MaxWindow ||
		query.Budget.WindowSeconds != int(window/time.Second) ||
		query.Budget.MaxSignalsPerSource < 1 || query.Budget.MaxSignalsPerSource > correlation.MaxSignalsPerSource ||
		query.Budget.PerSourceTimeoutMillis < 1 || query.Budget.PerSourceTimeoutMillis > int(correlation.MaxPerSourceTimeout/time.Millisecond) {
		return nil, errors.New("invalid Prometheus query budget")
	}

	// Leave time for the correlation service to return successful evidence when one query times out.
	timeout := time.Duration(query.Budget.PerSourceTimeoutMillis) * time.Millisecond * 9 / 10
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var token string
	if s.tokenFile != "" {
		data, err := os.ReadFile(s.tokenFile)
		token = strings.TrimSpace(string(data))
		if err != nil || token == "" || len(token) > 8192 || strings.IndexFunc(token, func(r rune) bool {
			return r < '!' || r > '~'
		}) >= 0 {
			return nil, errors.New("read Prometheus bearer token")
		}
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
	resource := scope.Resource
	if scope.Cluster != finding.LocalCluster || len(resource.Name) > 253 || !objectName.MatchString(resource.Name) {
		return "", errors.New("invalid Prometheus resource scope")
	}
	var matchers []string
	if resource.Kind == "Node" {
		if scope.Namespace != "" || resource.Namespace != "" {
			return "", errors.New("invalid Prometheus node scope")
		}
		matchers = append(matchers, "node="+strconv.Quote(resource.Name))
	} else {
		if scope.Namespace != resource.Namespace || len(scope.Namespace) > 63 || !objectName.MatchString(scope.Namespace) {
			return "", errors.New("invalid Prometheus namespace scope")
		}
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
	endpoint := s.baseURL.JoinPath("api", "v1", "query_range")
	if metric.instant {
		endpoint = s.baseURL.JoinPath("api", "v1", "query")
		params.Set("time", end.UTC().Format(time.RFC3339Nano))
	} else {
		params.Set("start", start.UTC().Format(time.RFC3339Nano))
		params.Set("end", end.UTC().Format(time.RFC3339Nano))
		params.Set("step", strconv.FormatInt(int64(rangeStep/time.Second), 10))
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return correlation.CorrelationSignal{}, errQuery
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := s.client.Do(req)
	if err != nil {
		return correlation.CorrelationSignal{}, errQuery
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return correlation.CorrelationSignal{}, errQuery
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return correlation.CorrelationSignal{}, errQuery
	}
	return decodeSignal(body, metric, start, end, resource)
}
