package sanitizer

import (
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

func TestCorrelationBundleSanitizesSummaryAndPreservesBoundedShape(t *testing.T) {
	secret := "very-secret-bearer-value"
	bundle := validCorrelationBundle()
	bundle.Signals = []correlation.CorrelationSignal{{
		ID:          "log-fingerprint-1",
		Source:      correlation.SourceVictoriaLogs,
		Type:        correlation.SignalTypeLog,
		Severity:    finding.SeverityWarning,
		Fingerprint: "sha256:abc123",
		Summary:     "Authorization: Bearer " + secret + " <script>alert(1)</script>",
	}}
	bundle.Sources[2].State = correlation.SourceAvailable

	safe, err := Default().(CorrelationSanitizer).CorrelationBundle(bundle)
	if err != nil {
		t.Fatalf("CorrelationBundle() error = %v", err)
	}
	if len(safe.Signals) != 1 {
		t.Fatalf("signal count = %d, want 1", len(safe.Signals))
	}
	summary := safe.Signals[0].Summary
	if strings.Contains(summary, secret) || strings.Contains(strings.ToLower(summary), "<script") {
		t.Fatalf("unsafe correlation summary crossed sanitizer boundary: %q", summary)
	}
	if !strings.Contains(summary, "[REDACTED:credential]") || !strings.Contains(summary, "[REDACTED:active-content]") {
		t.Fatalf("missing sanitizer markers in %q", summary)
	}
}

func TestCorrelationBundleRejectsInactiveSourceSignal(t *testing.T) {
	bundle := validCorrelationBundle()
	bundle.Signals = []correlation.CorrelationSignal{{ID: "metric-1", Source: correlation.SourcePrometheus, Type: correlation.SignalTypeMetric}}
	if _, err := DefaultCorrelation().CorrelationBundle(bundle); err == nil {
		t.Fatal("CorrelationBundle() accepted a signal from a disabled source")
	}
}

func TestCorrelationBundleRejectsSourceTypeSpoofing(t *testing.T) {
	bundle := validCorrelationBundle()
	bundle.Sources[1].State = correlation.SourceAvailable
	bundle.Signals = []correlation.CorrelationSignal{{ID: "spoofed-1", Source: correlation.SourcePrometheus, Type: correlation.SignalTypeLog}}
	if _, err := DefaultCorrelation().CorrelationBundle(bundle); err == nil {
		t.Fatal("CorrelationBundle() accepted source/type spoofing")
	}
}

func TestCorrelationBundleRejectsLegacyLokiSource(t *testing.T) {
	bundle := validCorrelationBundle()
	bundle.Sources[2].Source = correlation.CorrelationSource("loki")
	if _, err := DefaultCorrelation().CorrelationBundle(bundle); err == nil {
		t.Fatal("CorrelationBundle() accepted the removed Loki source")
	}
}

func validCorrelationBundle() correlation.CorrelationBundle {
	return correlation.CorrelationBundle{
		FindingID: "finding-1",
		Scope:     correlation.CorrelationScope{Cluster: "local", Namespace: "prod", Resource: finding.ResourceRef{APIVersion: "v1", Kind: "Pod", Namespace: "prod", Name: "demo"}},
		Window:    correlation.TimeWindow{Start: "2026-09-07T01:30:00Z", End: "2026-09-07T02:00:00Z"},
		Budget:    correlation.CorrelationBudget{WindowSeconds: 1800, PerSourceTimeoutMillis: 4000, MaxSignalsPerSource: 50},
		Sources: []correlation.SourceStatus{
			{Source: correlation.SourceKubernetesEvents, State: correlation.SourceDisabled},
			{Source: correlation.SourcePrometheus, State: correlation.SourceDisabled},
			{Source: correlation.SourceVictoriaLogs, State: correlation.SourceDisabled},
			{Source: correlation.SourceAlertmanager, State: correlation.SourceDisabled},
		},
		Signals: []correlation.CorrelationSignal{},
	}
}
