package correlation

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/finding"
)

func TestServiceCorrelationOutcomes(t *testing.T) {
	anchor := time.Date(2026, 9, 7, 2, 0, 0, 0, time.UTC)
	request := Request{
		FindingID:  "finding-1",
		Scope:      CorrelationScope{Cluster: "local", Namespace: "prod"},
		AnchorTime: anchor.Format(time.RFC3339),
	}

	tests := []struct {
		name      string
		options   ServiceOptions
		wantErr   error
		wantState map[CorrelationSource]SourceState
		wantCount int
	}{
		{
			name: "all sources disabled is an honest empty bundle",
			wantState: map[CorrelationSource]SourceState{
				SourceKubernetesEvents: SourceDisabled,
				SourcePrometheus:       SourceDisabled,
				SourceVictoriaLogs:     SourceDisabled,
				SourceAlertmanager:     SourceDisabled,
			},
		},
		{
			name: "partial source failure preserves safe evidence",
			options: ServiceOptions{
				Events: EventSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
					return []CorrelationSignal{{ID: "event-1"}}, errors.New("fixture partial failure")
				}),
				Prometheus: MetricSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
					return []CorrelationSignal{{ID: "metric-1"}}, nil
				}),
			},
			wantState: map[CorrelationSource]SourceState{
				SourceKubernetesEvents: SourcePartial,
				SourcePrometheus:       SourceAvailable,
				SourceVictoriaLogs:     SourceDisabled,
				SourceAlertmanager:     SourceDisabled,
			},
			wantCount: 2,
		},
		{
			name: "all configured sources unavailable fails closed",
			options: ServiceOptions{
				Events: EventSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
					return nil, errors.New("events unavailable")
				}),
				Prometheus: MetricSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
					return nil, errors.New("prometheus unavailable")
				}),
			},
			wantErr: ErrUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.options.Now = func() time.Time { return anchor }
			service, err := NewService(tc.options)
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			bundle, err := service.Correlate(context.Background(), request)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Correlate() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if bundle.FindingID != request.FindingID || bundle.Scope != request.Scope {
				t.Fatalf("bundle target drift: %#v", bundle)
			}
			if bundle.Window.End != anchor.Format(time.RFC3339) || bundle.Window.Start != anchor.Add(-DefaultWindow).Format(time.RFC3339) {
				t.Fatalf("unexpected bounded window: %#v", bundle.Window)
			}
			if bundle.Budget.WindowSeconds != int(DefaultWindow/time.Second) ||
				bundle.Budget.PerSourceTimeoutMillis != int(DefaultPerSourceTimeout/time.Millisecond) ||
				bundle.Budget.MaxSignalsPerSource != DefaultMaxSignalsPerSource {
				t.Fatalf("unexpected budget: %#v", bundle.Budget)
			}
			states := map[CorrelationSource]SourceState{}
			for _, status := range bundle.Sources {
				states[status.Source] = status.State
			}
			for source, want := range tc.wantState {
				if got := states[source]; got != want {
					t.Fatalf("source %q state = %q, want %q", source, got, want)
				}
			}
			if len(bundle.Signals) != tc.wantCount {
				t.Fatalf("signal count = %d, want %d", len(bundle.Signals), tc.wantCount)
			}
			for _, signal := range bundle.Signals {
				switch signal.ID {
				case "event-1":
					if signal.Source != SourceKubernetesEvents || signal.Type != SignalTypeEvent {
						t.Fatalf("event signal not normalized: %#v", signal)
					}
				case "metric-1":
					if signal.Source != SourcePrometheus || signal.Type != SignalTypeMetric {
						t.Fatalf("metric signal not normalized: %#v", signal)
					}
				}
			}
		})
	}
}

func TestServiceSourceTimeout(t *testing.T) {
	service, err := NewService(ServiceOptions{
		Timeout: 10 * time.Millisecond,
		Events: EventSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
			// Deliberately ignore the supplied context to prove that the service-owned budget,
			// rather than adapter cooperation, bounds request latency.
			time.Sleep(250 * time.Millisecond)
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	started := time.Now()
	_, err = service.Correlate(context.Background(), Request{FindingID: "finding-1", Scope: CorrelationScope{Cluster: "local"}})
	elapsed := time.Since(started)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Correlate() error = %v, want ErrUnavailable", err)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("Correlate() exceeded hard source timeout: elapsed=%s", elapsed)
	}
}

func TestServiceTruncatesAdapterOutputWithinBudget(t *testing.T) {
	service, err := NewService(ServiceOptions{
		MaxSignals: 2,
		VictoriaLogs: LogSignalSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
			return []CorrelationSignal{{ID: "log-1"}, {ID: "log-2"}, {ID: "log-3"}}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	bundle, err := service.Correlate(context.Background(), Request{FindingID: "finding-1", Scope: CorrelationScope{Cluster: "local"}})
	if err != nil {
		t.Fatalf("Correlate() error = %v", err)
	}
	if len(bundle.Signals) != 2 {
		t.Fatalf("signal count = %d, want 2", len(bundle.Signals))
	}
	for _, status := range bundle.Sources {
		if status.Source == SourceVictoriaLogs && status.State != SourcePartial {
			t.Fatalf("VictoriaLogs state = %q, want partial", status.State)
		}
	}
}

func TestNewServiceRejectsUnboundedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options ServiceOptions
	}{
		{name: "window", options: ServiceOptions{Window: MaxWindow + time.Second}},
		{name: "timeout", options: ServiceOptions{Timeout: MaxPerSourceTimeout + time.Second}},
		{name: "signals", options: ServiceOptions{MaxSignals: MaxSignalsPerSource + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewService(tc.options); err == nil {
				t.Fatal("NewService() unexpectedly accepted unbounded configuration")
			}
		})
	}
}

func TestServiceRanksAndDeduplicatesEvidenceBeforeBudget(t *testing.T) {
	target := finding.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "checkout"}
	related := finding.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "worker"}
	older := "2026-09-07T01:58:00Z"
	newer := "2026-09-07T01:59:00Z"
	events := []CorrelationSignal{
		{ID: "related-critical", Resource: &related, Severity: finding.SeverityCritical, LastSeen: newer},
		{ID: "duplicate-old", Resource: &target, Severity: finding.SeverityWarning, LastSeen: older, Fingerprint: "same", Count: 4},
		{ID: "exact-info", Resource: &target, Severity: finding.SeverityInfo, LastSeen: newer},
		{ID: "duplicate-new", Resource: &target, Severity: finding.SeverityWarning, LastSeen: newer, Fingerprint: "same", Count: 6},
	}
	service, err := NewService(ServiceOptions{
		MaxSignals: 2,
		Events: EventSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
			return events, nil
		}),
		VictoriaLogs: LogSignalSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
			return []CorrelationSignal{{ID: "exact-log", Resource: &target, Severity: finding.SeverityCritical, LastSeen: older, Fingerprint: "same"}}, nil
		}),
		Alertmanager: AlertSourceFunc(func(context.Context, Query) ([]CorrelationSignal, error) {
			return []CorrelationSignal{{ID: "exact-alert", Resource: &target, Severity: finding.SeverityWarning, FirstSeen: older}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		FindingID: "checkout-finding", AnchorTime: "2026-09-07T02:00:00Z",
		Scope: CorrelationScope{Cluster: "local", Namespace: "prod", Resource: target},
	}
	for range 2 {
		bundle, err := service.Correlate(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(bundle.Signals))
		for _, signal := range bundle.Signals {
			got = append(got, signal.ID)
		}
		want := []string{"exact-log", "duplicate-new", "exact-alert", "exact-info"}
		if !slices.Equal(got, want) {
			t.Fatalf("evidence order = %v, want %v", got, want)
		}
		if bundle.Signals[1].Count != 6 || bundle.Sources[0].State != SourcePartial {
			t.Fatalf("duplicate counts or source state changed: %#v", bundle)
		}
		slices.Reverse(events)
	}
}
