package audit

import (
	"context"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
)

type recorderContextKey struct{}

// Recorder holds bounded request-local audit state. It is safe for concurrent annotation,
// although the normal HTTP middleware path mutates it serially.
type Recorder struct {
	mu                sync.Mutex
	routePattern      string
	capability        authorization.Capability
	principalSubject  string
	principalProvider string
	cluster           string
	namespace         string
	outcome           Outcome
}

func NewRecorder(routePattern string, capability authorization.Capability) *Recorder {
	return &Recorder{routePattern: routePattern, capability: capability}
}

func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderContextKey{}, recorder)
}

func RecorderFromContext(ctx context.Context) (*Recorder, bool) {
	recorder, ok := ctx.Value(recorderContextKey{}).(*Recorder)
	return recorder, ok && recorder != nil
}

func (r *Recorder) SetPrincipal(subject, provider string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.principalSubject = subject
	r.principalProvider = provider
}

// SetScope records only validated normalized authorization scope. Invalid scope is omitted rather
// than copied into an audit event, preserving the allowlisted event boundary for hostile input.
func (r *Recorder) SetScope(scope authorization.Scope) {
	if r == nil || scope.Validate() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cluster = scope.Cluster
	r.namespace = scope.Namespace
}

func (r *Recorder) SetOutcome(outcome Outcome) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcome = outcome
}

func (r *Recorder) Outcome() Outcome {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outcome
}

func (r *Recorder) Event(timestamp time.Time, requestID, correlationID string, status int, latency time.Duration) Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	latencyMS := latency.Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}
	if latencyMS > maxLatencyMS {
		latencyMS = maxLatencyMS
	}

	return Event{
		Timestamp:         timestamp.UTC(),
		RequestID:         requestID,
		CorrelationID:     correlationID,
		RoutePattern:      r.routePattern,
		Capability:        r.capability,
		PrincipalSubject:  r.principalSubject,
		PrincipalProvider: r.principalProvider,
		Cluster:           r.cluster,
		Namespace:         r.namespace,
		Outcome:           r.outcome,
		HTTPStatus:        status,
		LatencyMS:         latencyMS,
	}
}
