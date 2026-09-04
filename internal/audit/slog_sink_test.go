package audit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/internal/authorization"
)

func TestSlogSinkRecordsOnlyValidatedAuditFields(t *testing.T) {
	var output bytes.Buffer
	sink := NewSlogSink(slog.New(slog.NewTextHandler(&output, nil)))
	event := Event{
		Timestamp:         time.Unix(1, 0).UTC(),
		RequestID:         "req-1",
		CorrelationID:     "corr-1",
		RoutePattern:      "GET /api/v1/clusters",
		Capability:        authorization.CapabilityClustersList,
		PrincipalSubject:  "alice",
		PrincipalProvider: "test-provider",
		Outcome:           OutcomeSuccess,
		HTTPStatus:        200,
		LatencyMS:         3,
	}
	if err := sink.Record(context.Background(), event); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	text := output.String()
	for _, expected := range []string{"audit event", "request_id=req-1", "capability=clusters:list", "outcome=success", "http_status=200"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in log: %s", expected, text)
		}
	}
}

func TestSlogSinkRejectsInvalidEvent(t *testing.T) {
	sink := NewSlogSink(nil)
	if err := sink.Record(context.Background(), Event{}); err == nil {
		t.Fatal("expected invalid event rejection")
	}
}
