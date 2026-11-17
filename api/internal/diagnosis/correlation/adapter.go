package correlation

import "context"

type Query struct {
	Scope  CorrelationScope
	Window TimeWindow
	Budget CorrelationBudget
}

type EventSource interface {
	Events(context.Context, Query) ([]CorrelationSignal, error)
}

type EventSourceFunc func(context.Context, Query) ([]CorrelationSignal, error)

func (f EventSourceFunc) Events(ctx context.Context, query Query) ([]CorrelationSignal, error) {
	return f(ctx, query)
}

type MetricSource interface {
	Metrics(context.Context, Query) ([]CorrelationSignal, error)
}

type MetricSourceFunc func(context.Context, Query) ([]CorrelationSignal, error)

func (f MetricSourceFunc) Metrics(ctx context.Context, query Query) ([]CorrelationSignal, error) {
	return f(ctx, query)
}

type LogSignalSource interface {
	LogSignals(context.Context, Query) ([]CorrelationSignal, error)
}

type LogSignalSourceFunc func(context.Context, Query) ([]CorrelationSignal, error)

func (f LogSignalSourceFunc) LogSignals(ctx context.Context, query Query) ([]CorrelationSignal, error) {
	return f(ctx, query)
}

type AlertSource interface {
	Alerts(context.Context, Query) ([]CorrelationSignal, error)
}

type AlertSourceFunc func(context.Context, Query) ([]CorrelationSignal, error)

func (f AlertSourceFunc) Alerts(ctx context.Context, query Query) ([]CorrelationSignal, error) {
	return f(ctx, query)
}
