package correlation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	DefaultWindow              = 30 * time.Minute
	MaxWindow                  = 2 * time.Hour
	DefaultPerSourceTimeout    = 4 * time.Second
	MaxPerSourceTimeout        = 10 * time.Second
	DefaultMaxSignalsPerSource = 50
	MaxSignalsPerSource        = 100
	SourceCount                = 4
	MaxTotalSignals            = SourceCount * MaxSignalsPerSource
)

var (
	ErrUnavailable    = errors.New("correlation unavailable")
	ErrSourceDisabled = errors.New("correlation source disabled")
)

type Correlator interface {
	Correlate(context.Context, Request) (CorrelationBundle, error)
}

type CorrelatorFunc func(context.Context, Request) (CorrelationBundle, error)

func (f CorrelatorFunc) Correlate(ctx context.Context, request Request) (CorrelationBundle, error) {
	return f(ctx, request)
}

type ServiceOptions struct {
	Events       EventSource
	Prometheus   MetricSource
	Loki         LogSignalSource
	Alertmanager AlertSource
	Window       time.Duration
	Timeout      time.Duration
	MaxSignals   int
	Now          func() time.Time
}

type Service struct {
	events       EventSource
	prometheus   MetricSource
	loki         LogSignalSource
	alertmanager AlertSource
	window       time.Duration
	timeout      time.Duration
	maxSignals   int
	now          func() time.Time
}

func NewService(options ServiceOptions) (*Service, error) {
	window := options.Window
	if window == 0 {
		window = DefaultWindow
	}
	if window <= 0 || window > MaxWindow {
		return nil, fmt.Errorf("correlation window must be between 1ns and %s", MaxWindow)
	}

	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultPerSourceTimeout
	}
	if timeout <= 0 || timeout > MaxPerSourceTimeout {
		return nil, fmt.Errorf("correlation source timeout must be between 1ns and %s", MaxPerSourceTimeout)
	}

	maxSignals := options.MaxSignals
	if maxSignals == 0 {
		maxSignals = DefaultMaxSignalsPerSource
	}
	if maxSignals < 1 || maxSignals > MaxSignalsPerSource {
		return nil, fmt.Errorf("correlation max signals per source must be between 1 and %d", MaxSignalsPerSource)
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		events:       options.Events,
		prometheus:   options.Prometheus,
		loki:         options.Loki,
		alertmanager: options.Alertmanager,
		window:       window,
		timeout:      timeout,
		maxSignals:   maxSignals,
		now:          now,
	}, nil
}

func Disabled() Correlator {
	service, err := NewService(ServiceOptions{})
	if err != nil {
		panic(err)
	}
	return service
}

type sourceResult struct {
	index      int
	configured bool
	status     SourceStatus
	signals    []CorrelationSignal
}

type sourceCall struct {
	source     CorrelationSource
	signalType SignalType
	configured bool
	call       func(context.Context, Query) ([]CorrelationSignal, error)
}

func (s *Service) Correlate(ctx context.Context, request Request) (CorrelationBundle, error) {
	end := s.correlationEnd(request.AnchorTime)
	window := TimeWindow{
		Start: end.Add(-s.window).UTC().Format(time.RFC3339),
		End:   end.UTC().Format(time.RFC3339),
	}
	budget := CorrelationBudget{
		WindowSeconds:          int(s.window / time.Second),
		PerSourceTimeoutMillis: int(s.timeout / time.Millisecond),
		MaxSignalsPerSource:    s.maxSignals,
	}
	query := Query{Scope: request.Scope, Window: window, Budget: budget}

	calls := []sourceCall{
		{
			source: SourceKubernetesEvents, signalType: SignalTypeEvent,
			configured: s.events != nil,
			call: func(callCtx context.Context, q Query) ([]CorrelationSignal, error) {
				if s.events == nil {
					return nil, ErrSourceDisabled
				}
				return s.events.Events(callCtx, q)
			},
		},
		{
			source: SourcePrometheus, signalType: SignalTypeMetric,
			configured: s.prometheus != nil,
			call: func(callCtx context.Context, q Query) ([]CorrelationSignal, error) {
				if s.prometheus == nil {
					return nil, ErrSourceDisabled
				}
				return s.prometheus.Metrics(callCtx, q)
			},
		},
		{
			source: SourceLoki, signalType: SignalTypeLog,
			configured: s.loki != nil,
			call: func(callCtx context.Context, q Query) ([]CorrelationSignal, error) {
				if s.loki == nil {
					return nil, ErrSourceDisabled
				}
				return s.loki.LogSignals(callCtx, q)
			},
		},
		{
			source: SourceAlertmanager, signalType: SignalTypeAlert,
			configured: s.alertmanager != nil,
			call: func(callCtx context.Context, q Query) ([]CorrelationSignal, error) {
				if s.alertmanager == nil {
					return nil, ErrSourceDisabled
				}
				return s.alertmanager.Alerts(callCtx, q)
			},
		},
	}

	results := make(chan sourceResult, len(calls))
	for index, current := range calls {
		go s.collectSource(ctx, index, current, query, results)
	}

	ordered := make([]sourceResult, len(calls))
	for range calls {
		result := <-results
		ordered[result.index] = result
	}

	bundle := CorrelationBundle{
		FindingID: request.FindingID,
		Scope:     request.Scope,
		Window:    window,
		Budget:    budget,
		Sources:   make([]SourceStatus, 0, len(calls)),
		Signals:   make([]CorrelationSignal, 0),
	}

	configured := 0
	unavailable := 0
	for _, result := range ordered {
		bundle.Sources = append(bundle.Sources, result.status)
		bundle.Signals = append(bundle.Signals, result.signals...)
		if result.configured {
			configured++
			if result.status.State == SourceUnavailable {
				unavailable++
			}
		}
	}

	if configured > 0 && unavailable == configured && len(bundle.Signals) == 0 {
		return CorrelationBundle{}, ErrUnavailable
	}
	return bundle, nil
}

func (s *Service) collectSource(
	parent context.Context,
	index int,
	current sourceCall,
	query Query,
	results chan<- sourceResult,
) {
	if !current.configured {
		results <- sourceResult{
			index:      index,
			configured: false,
			status:     SourceStatus{Source: current.source, State: SourceDisabled},
			signals:    []CorrelationSignal{},
		}
		return
	}

	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()

	signals, err := current.call(ctx, query)
	state := SourceAvailable
	if len(signals) > s.maxSignals {
		signals = signals[:s.maxSignals]
		state = SourcePartial
	}
	if err != nil {
		if errors.Is(err, ErrSourceDisabled) {
			state = SourceDisabled
			signals = nil
		} else if len(signals) > 0 {
			state = SourcePartial
		} else {
			state = SourceUnavailable
		}
	}

	normalized := make([]CorrelationSignal, len(signals))
	copy(normalized, signals)
	for i := range normalized {
		normalized[i].Source = current.source
		normalized[i].Type = current.signalType
	}

	results <- sourceResult{
		index:      index,
		configured: current.configured,
		status:     SourceStatus{Source: current.source, State: state},
		signals:    normalized,
	}
}

func (s *Service) correlationEnd(anchor string) time.Time {
	if parsed, err := time.Parse(time.RFC3339, anchor); err == nil {
		return parsed
	}
	return s.now().UTC()
}
