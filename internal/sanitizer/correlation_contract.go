package sanitizer

import "github.com/abchw517/kube-aiops/internal/correlation"

// CorrelationSanitizer is a Phase 2 extension boundary kept separate from the Phase 1.4 Sanitizer
// interface so existing typed sanitizer integrations do not gain an accidental compatibility break.
type CorrelationSanitizer interface {
	CorrelationBundle(correlation.CorrelationBundle) (correlation.CorrelationBundle, error)
}

func DefaultCorrelation() CorrelationSanitizer {
	base := Default()
	correlationSanitizer, ok := base.(CorrelationSanitizer)
	if !ok {
		panic("default sanitizer does not implement correlation sanitization")
	}
	return correlationSanitizer
}
