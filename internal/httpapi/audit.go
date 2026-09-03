package httpapi

import (
	"net/http"
	"time"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
)

type auditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditResponseWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(payload)
}

func (w *auditResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *auditResponseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// auditMiddleware wraps only known protected routes. It asks the real ServeMux for its canonical
// matched pattern before AuthN runs, so audit never needs to persist a raw URL or query string.
func (s *Server) auditMiddleware(matcher *http.ServeMux, sink internalaudit.Sink, next http.Handler) http.Handler {
	if sink == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := matcher.Handler(r)
		route, ok := protectedRouteFor(pattern)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		startedAt := time.Now()
		recorder := internalaudit.NewRecorder(route.pattern, route.capability)
		ctx := internalaudit.WithRecorder(r.Context(), recorder)
		captured := &auditResponseWriter{ResponseWriter: w}

		next.ServeHTTP(captured, r.WithContext(ctx))

		if recorder.Outcome() == "" {
			recorder.SetOutcome(auditOutcomeForStatus(captured.Status()))
		}
		event := recorder.Event(
			startedAt.UTC(),
			RequestIDFromContext(ctx),
			CorrelationIDFromContext(ctx),
			captured.Status(),
			time.Since(startedAt),
		)
		if err := event.Validate(); err != nil {
			s.logAuditFailure(event, "invalid_event")
			return
		}
		if err := sink.Record(ctx, event); err != nil {
			s.logAuditFailure(event, "sink_error")
		}
	})
}

func auditOutcomeForStatus(status int) internalaudit.Outcome {
	switch {
	case status >= 200 && status < 400:
		return internalaudit.OutcomeSuccess
	case status == http.StatusUnauthorized:
		return internalaudit.OutcomeUnauthenticated
	case status == http.StatusForbidden:
		return internalaudit.OutcomeDenied
	case status == http.StatusNotFound:
		return internalaudit.OutcomeNotFound
	case status >= 400 && status < 500:
		return internalaudit.OutcomeInvalidRequest
	default:
		return internalaudit.OutcomeBackendError
	}
}

func (s *Server) logAuditFailure(event internalaudit.Event, reason string) {
	s.logger.Warn(
		"audit delivery failed",
		"reason", reason,
		"request_id", event.RequestID,
		"correlation_id", event.CorrelationID,
		"capability", event.Capability,
	)
}
