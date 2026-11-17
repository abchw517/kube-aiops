package audit

import (
	"context"
	"fmt"
	"log/slog"
)

type SlogSink struct {
	logger *slog.Logger
}

func NewSlogSink(logger *slog.Logger) Sink {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogSink{logger: logger}
}

func (s *SlogSink) Record(ctx context.Context, event Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate audit event: %w", err)
	}
	s.logger.InfoContext(
		ctx,
		"audit event",
		"timestamp", event.Timestamp,
		"request_id", event.RequestID,
		"correlation_id", event.CorrelationID,
		"route", event.RoutePattern,
		"capability", event.Capability,
		"principal_subject", event.PrincipalSubject,
		"principal_provider", event.PrincipalProvider,
		"cluster", event.Cluster,
		"namespace", event.Namespace,
		"outcome", event.Outcome,
		"http_status", event.HTTPStatus,
		"latency_ms", event.LatencyMS,
	)
	return nil
}
