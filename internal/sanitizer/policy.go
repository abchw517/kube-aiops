package sanitizer

import (
	"fmt"

	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

const truncationMarker = " [TRUNCATED]"

type Policy struct {
	MaxProblemBytes        int
	MaxDetailsBytes        int
	MaxPageDiagnosticBytes int
	MaxIdentifierBytes     int
	MaxAPIVersionBytes     int
	MaxKindBytes           int
	MaxSeverityBytes       int
	MaxSourceBytes         int
	MaxCreatedAtBytes      int
	MaxCursorBytes         int
	MaxStatusBytes         int
	MaxSummaryKeys         int
}

func DefaultPolicy() Policy {
	return Policy{
		MaxProblemBytes:        2 << 10,
		MaxDetailsBytes:        8 << 10,
		MaxPageDiagnosticBytes: 512 << 10,
		MaxIdentifierBytes:     253,
		MaxAPIVersionBytes:     128,
		MaxKindBytes:           128,
		MaxSeverityBytes:       32,
		MaxSourceBytes:         64,
		MaxCreatedAtBytes:      64,
		MaxCursorBytes:         1024,
		MaxStatusBytes:         128,
		MaxSummaryKeys:         512,
	}
}

func (p Policy) Validate() error {
	values := map[string]int{
		"problem":        p.MaxProblemBytes,
		"details":        p.MaxDetailsBytes,
		"pageDiagnostic": p.MaxPageDiagnosticBytes,
		"identifier":     p.MaxIdentifierBytes,
		"apiVersion":     p.MaxAPIVersionBytes,
		"kind":           p.MaxKindBytes,
		"severity":       p.MaxSeverityBytes,
		"source":         p.MaxSourceBytes,
		"createdAt":      p.MaxCreatedAtBytes,
		"cursor":         p.MaxCursorBytes,
		"status":         p.MaxStatusBytes,
		"summaryKeys":    p.MaxSummaryKeys,
	}
	for name, value := range values {
		if value <= 0 {
			return fmt.Errorf("%w: %s must be positive", ErrInvalidPolicy, name)
		}
	}
	if p.MaxProblemBytes <= len(truncationMarker) || p.MaxDetailsBytes <= len(truncationMarker) {
		return fmt.Errorf("%w: diagnostic limits must exceed truncation marker", ErrInvalidPolicy)
	}
	if p.MaxPageDiagnosticBytes < p.MaxProblemBytes+p.MaxDetailsBytes {
		return fmt.Errorf("%w: page diagnostic limit is too small", ErrInvalidPolicy)
	}
	if p.MaxSummaryKeys < 3 {
		return fmt.Errorf("%w: summary key limit is too small", ErrInvalidPolicy)
	}
	return nil
}

type Sanitizer interface {
	Finding(finding.Finding) (finding.Finding, error)
	FindingPage(finding.Page) (finding.Page, error)
	FindingSummary(finding.Summary) (finding.Summary, error)
	Resource(kubernetes.ResourceDetail) (kubernetes.ResourceDetail, error)
}

type typedSanitizer struct {
	policy Policy
}

func New(policy Policy) (Sanitizer, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &typedSanitizer{policy: policy}, nil
}

func Default() Sanitizer {
	s, err := New(DefaultPolicy())
	if err != nil {
		panic(err)
	}
	return s
}
