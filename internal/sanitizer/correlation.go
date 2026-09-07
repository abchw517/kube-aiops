package sanitizer

import (
	"fmt"
	"math"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

const maxEvidenceRefsPerSignal = 16

func (s *typedSanitizer) CorrelationBundle(bundle correlation.CorrelationBundle) (correlation.CorrelationBundle, error) {
	if err := validateBoundedField("correlation.findingId", bundle.FindingID, s.policy.MaxIdentifierBytes, true); err != nil {
		return correlation.CorrelationBundle{}, err
	}
	if err := validateBoundedField("correlation.scope.cluster", bundle.Scope.Cluster, s.policy.MaxIdentifierBytes, true); err != nil {
		return correlation.CorrelationBundle{}, err
	}
	if err := validateBoundedField("correlation.scope.namespace", bundle.Scope.Namespace, s.policy.MaxIdentifierBytes, false); err != nil {
		return correlation.CorrelationBundle{}, err
	}
	if err := s.validateResourceRef(bundle.Scope.Resource); err != nil {
		return correlation.CorrelationBundle{}, err
	}
	if bundle.Scope.Namespace != "" && bundle.Scope.Resource.Namespace != "" && bundle.Scope.Namespace != bundle.Scope.Resource.Namespace {
		return correlation.CorrelationBundle{}, fmt.Errorf("%w: correlation scope namespace mismatch", ErrUnsafeField)
	}

	start, err := s.validateCorrelationTimestamp("correlation.window.start", bundle.Window.Start, true)
	if err != nil {
		return correlation.CorrelationBundle{}, err
	}
	end, err := s.validateCorrelationTimestamp("correlation.window.end", bundle.Window.End, true)
	if err != nil {
		return correlation.CorrelationBundle{}, err
	}
	if end.Before(start) || end.Sub(start) > correlation.MaxWindow {
		return correlation.CorrelationBundle{}, fmt.Errorf("%w: correlation window", ErrUnsafeField)
	}

	if bundle.Budget.WindowSeconds < 1 || bundle.Budget.WindowSeconds > int(correlation.MaxWindow/time.Second) ||
		bundle.Budget.PerSourceTimeoutMillis < 1 || bundle.Budget.PerSourceTimeoutMillis > int(correlation.MaxPerSourceTimeout/time.Millisecond) ||
		bundle.Budget.MaxSignalsPerSource < 1 || bundle.Budget.MaxSignalsPerSource > correlation.MaxSignalsPerSource {
		return correlation.CorrelationBundle{}, fmt.Errorf("%w: correlation budget", ErrUnsafeField)
	}
	if int(end.Sub(start)/time.Second) != bundle.Budget.WindowSeconds {
		return correlation.CorrelationBundle{}, fmt.Errorf("%w: correlation window budget mismatch", ErrUnsafeField)
	}

	if len(bundle.Sources) != correlation.SourceCount {
		return correlation.CorrelationBundle{}, fmt.Errorf("%w: correlation source status count", ErrUnsafeField)
	}
	states := make(map[correlation.CorrelationSource]correlation.SourceState, correlation.SourceCount)
	safeSources := make([]correlation.SourceStatus, len(bundle.Sources))
	for i, status := range bundle.Sources {
		if !validCorrelationSource(status.Source) || !validSourceState(status.State) {
			return correlation.CorrelationBundle{}, fmt.Errorf("%w: correlation source status", ErrUnsafeField)
		}
		if _, duplicate := states[status.Source]; duplicate {
			return correlation.CorrelationBundle{}, fmt.Errorf("%w: duplicate correlation source status", ErrUnsafeField)
		}
		states[status.Source] = status.State
		safeSources[i] = status
	}
	for _, source := range allCorrelationSources() {
		if _, ok := states[source]; !ok {
			return correlation.CorrelationBundle{}, fmt.Errorf("%w: missing correlation source status", ErrUnsafeField)
		}
	}

	if len(bundle.Signals) > correlation.MaxTotalSignals {
		return correlation.CorrelationBundle{}, fmt.Errorf("%w: correlation signal count", ErrUnsafeField)
	}
	perSource := make(map[correlation.CorrelationSource]int, correlation.SourceCount)
	safeSignals := make([]correlation.CorrelationSignal, len(bundle.Signals))
	for i, signal := range bundle.Signals {
		safe, err := s.sanitizeCorrelationSignal(signal)
		if err != nil {
			return correlation.CorrelationBundle{}, err
		}
		state, ok := states[safe.Source]
		if !ok || state == correlation.SourceDisabled || state == correlation.SourceUnavailable {
			return correlation.CorrelationBundle{}, fmt.Errorf("%w: signal emitted by inactive source", ErrUnsafeField)
		}
		perSource[safe.Source]++
		if perSource[safe.Source] > bundle.Budget.MaxSignalsPerSource {
			return correlation.CorrelationBundle{}, fmt.Errorf("%w: per-source signal budget exceeded", ErrUnsafeField)
		}
		safeSignals[i] = safe
	}

	bundle.Sources = safeSources
	bundle.Signals = safeSignals
	return bundle, nil
}

func (s *typedSanitizer) sanitizeCorrelationSignal(signal correlation.CorrelationSignal) (correlation.CorrelationSignal, error) {
	if err := validateBoundedField("correlation.signal.id", signal.ID, s.policy.MaxIdentifierBytes, true); err != nil {
		return correlation.CorrelationSignal{}, err
	}
	if !validCorrelationSource(signal.Source) || !validSignalType(signal.Type) || !sourceMatchesSignalType(signal.Source, signal.Type) {
		return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation signal source/type", ErrUnsafeField)
	}
	if err := validateBoundedField("correlation.signal.severity", signal.Severity, s.policy.MaxSeverityBytes, false); err != nil {
		return correlation.CorrelationSignal{}, err
	}
	if signal.Severity != "" {
		switch signal.Severity {
		case finding.SeverityCritical, finding.SeverityWarning, finding.SeverityInfo:
		default:
			return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation signal severity", ErrUnsafeField)
		}
	}
	if signal.Resource != nil {
		if err := s.validateResourceRef(*signal.Resource); err != nil {
			return correlation.CorrelationSignal{}, err
		}
	}
	first, err := s.validateCorrelationTimestamp("correlation.signal.firstSeen", signal.FirstSeen, false)
	if err != nil {
		return correlation.CorrelationSignal{}, err
	}
	last, err := s.validateCorrelationTimestamp("correlation.signal.lastSeen", signal.LastSeen, false)
	if err != nil {
		return correlation.CorrelationSignal{}, err
	}
	if !first.IsZero() && !last.IsZero() && last.Before(first) {
		return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation signal time range", ErrUnsafeField)
	}
	if signal.Count < 0 {
		return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation signal count", ErrUnsafeField)
	}
	if err := validateBoundedField("correlation.signal.name", signal.Name, s.policy.MaxIdentifierBytes, false); err != nil {
		return correlation.CorrelationSignal{}, err
	}
	if signal.Value != nil && (math.IsNaN(*signal.Value) || math.IsInf(*signal.Value, 0)) {
		return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation signal value", ErrUnsafeField)
	}
	if signal.Unit != "" && !validSignalUnit(signal.Unit) {
		return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation signal unit", ErrUnsafeField)
	}
	if err := validateBoundedField("correlation.signal.fingerprint", signal.Fingerprint, s.policy.MaxIdentifierBytes, false); err != nil {
		return correlation.CorrelationSignal{}, err
	}
	if len(signal.Evidence) > maxEvidenceRefsPerSignal {
		return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation evidence count", ErrUnsafeField)
	}
	evidence := make([]correlation.EvidenceRef, len(signal.Evidence))
	for i, ref := range signal.Evidence {
		if !validCorrelationSource(ref.Source) {
			return correlation.CorrelationSignal{}, fmt.Errorf("%w: correlation evidence source", ErrUnsafeField)
		}
		if err := validateBoundedField("correlation.evidence.id", ref.ID, s.policy.MaxIdentifierBytes, true); err != nil {
			return correlation.CorrelationSignal{}, err
		}
		evidence[i] = ref
	}

	signal.Summary = sanitizeDiagnosticText(signal.Summary, s.policy.MaxDetailsBytes)
	signal.Evidence = evidence
	return signal, nil
}

func (s *typedSanitizer) validateCorrelationTimestamp(name, value string, required bool) (time.Time, error) {
	if err := validateBoundedField(name, value, s.policy.MaxCreatedAtBytes, required); err != nil {
		return time.Time{}, err
	}
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s", ErrUnsafeField, name)
	}
	return parsed, nil
}

func allCorrelationSources() []correlation.CorrelationSource {
	return []correlation.CorrelationSource{
		correlation.SourceKubernetesEvents,
		correlation.SourcePrometheus,
		correlation.SourceVictoriaLogs,
		correlation.SourceAlertmanager,
	}
}

func validCorrelationSource(source correlation.CorrelationSource) bool {
	switch source {
	case correlation.SourceKubernetesEvents, correlation.SourcePrometheus, correlation.SourceVictoriaLogs, correlation.SourceAlertmanager:
		return true
	default:
		return false
	}
}

func validSourceState(state correlation.SourceState) bool {
	switch state {
	case correlation.SourceAvailable, correlation.SourceUnavailable, correlation.SourceDisabled, correlation.SourcePartial:
		return true
	default:
		return false
	}
}

func validSignalType(signalType correlation.SignalType) bool {
	switch signalType {
	case correlation.SignalTypeEvent, correlation.SignalTypeMetric, correlation.SignalTypeLog, correlation.SignalTypeAlert:
		return true
	default:
		return false
	}
}

func sourceMatchesSignalType(source correlation.CorrelationSource, signalType correlation.SignalType) bool {
	return (source == correlation.SourceKubernetesEvents && signalType == correlation.SignalTypeEvent) ||
		(source == correlation.SourcePrometheus && signalType == correlation.SignalTypeMetric) ||
		(source == correlation.SourceVictoriaLogs && signalType == correlation.SignalTypeLog) ||
		(source == correlation.SourceAlertmanager && signalType == correlation.SignalTypeAlert)
}

func validSignalUnit(unit correlation.SignalUnit) bool {
	switch unit {
	case correlation.UnitCount,
		correlation.UnitBytes,
		correlation.UnitSeconds,
		correlation.UnitMilliseconds,
		correlation.UnitPercent,
		correlation.UnitRatio,
		correlation.UnitCores,
		correlation.UnitBytesPerSecond,
		correlation.UnitRequestsPerSecond:
		return true
	default:
		return false
	}
}
