package correlation

import (
	"cmp"
	"slices"
	"time"

	"github.com/abchw517/kube-aiops/internal/finding"
)

type rankedSignal struct {
	signal   CorrelationSignal
	exact    int
	severity int
	lastSeen time.Time
}

func rankSignals(signals []CorrelationSignal, target finding.ResourceRef) {
	if len(signals) < 2 {
		return
	}
	ranked := make([]rankedSignal, len(signals))
	for index, signal := range signals {
		entry := rankedSignal{signal: signal, severity: severityPriority(signal.Severity)}
		if target.Kind != "" && target.Name != "" && signal.Resource != nil &&
			signal.Resource.Kind == target.Kind && signal.Resource.Namespace == target.Namespace &&
			signal.Resource.Name == target.Name {
			entry.exact = 1
		}
		entry.lastSeen, _ = time.Parse(time.RFC3339Nano, signal.LastSeen)
		if entry.lastSeen.IsZero() {
			entry.lastSeen, _ = time.Parse(time.RFC3339Nano, signal.FirstSeen)
		}
		ranked[index] = entry
	}
	slices.SortStableFunc(ranked, func(a, b rankedSignal) int {
		return cmp.Or(
			cmp.Compare(b.exact, a.exact),
			cmp.Compare(b.severity, a.severity),
			b.lastSeen.Compare(a.lastSeen),
			cmp.Compare(a.signal.Source, b.signal.Source),
			cmp.Compare(a.signal.ID, b.signal.ID),
			cmp.Compare(b.signal.Count, a.signal.Count),
		)
	})
	for index, entry := range ranked {
		signals[index] = entry.signal
	}
}

func severityPriority(severity string) int {
	switch severity {
	case finding.SeverityCritical:
		return 3
	case finding.SeverityWarning:
		return 2
	case finding.SeverityInfo:
		return 1
	default:
		return 0
	}
}

type signalIdentity struct {
	source        CorrelationSource
	kind          string
	namespace     string
	resource      string
	key           string
	isFingerprint bool
}

func deduplicateSignals(ranked []CorrelationSignal) []CorrelationSignal {
	seen := make(map[signalIdentity]struct{}, len(ranked))
	unique := ranked[:0]
	for _, signal := range ranked {
		identity := signalIdentity{source: signal.Source, key: signal.ID}
		if signal.Resource != nil {
			identity.kind = signal.Resource.Kind
			identity.namespace = signal.Resource.Namespace
			identity.resource = signal.Resource.Name
		}
		if signal.Fingerprint != "" {
			identity.key = signal.Fingerprint
			identity.isFingerprint = true
		}
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		unique = append(unique, signal)
	}
	return unique
}
