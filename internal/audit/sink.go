package audit

import "context"

// Sink receives validated audit events through a provider-neutral boundary.
type Sink interface {
	Record(context.Context, Event) error
}

type SinkFunc func(context.Context, Event) error

func (f SinkFunc) Record(ctx context.Context, event Event) error {
	return f(ctx, event)
}
