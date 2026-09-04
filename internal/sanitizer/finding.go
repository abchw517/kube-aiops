package sanitizer

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/abchw517/kube-aiops/internal/finding"
)

func (s *typedSanitizer) Finding(item finding.Finding) (finding.Finding, error) {
	if err := validateBoundedField("finding.id", item.ID, s.policy.MaxIdentifierBytes, true); err != nil {
		return finding.Finding{}, err
	}
	if err := validateBoundedField("finding.cluster", item.Cluster, s.policy.MaxIdentifierBytes, true); err != nil {
		return finding.Finding{}, err
	}
	if err := validateBoundedField("finding.namespace", item.Namespace, s.policy.MaxIdentifierBytes, false); err != nil {
		return finding.Finding{}, err
	}
	if err := validateBoundedField("finding.severity", item.Severity, s.policy.MaxSeverityBytes, true); err != nil {
		return finding.Finding{}, err
	}
	switch item.Severity {
	case finding.SeverityCritical, finding.SeverityWarning, finding.SeverityInfo:
	default:
		return finding.Finding{}, fmt.Errorf("%w: finding.severity", ErrUnsafeField)
	}
	if err := validateBoundedField("finding.source", item.Source, s.policy.MaxSourceBytes, true); err != nil {
		return finding.Finding{}, err
	}
	if err := validateBoundedField("finding.createdAt", item.CreatedAt, s.policy.MaxCreatedAtBytes, false); err != nil {
		return finding.Finding{}, err
	}
	if err := s.validateResourceRef(item.Resource); err != nil {
		return finding.Finding{}, err
	}
	if item.Namespace != "" && item.Resource.Namespace != "" && item.Namespace != item.Resource.Namespace {
		return finding.Finding{}, fmt.Errorf("%w: finding namespace mismatch", ErrUnsafeField)
	}

	item.Problem = sanitizeDiagnosticText(item.Problem, s.policy.MaxProblemBytes)
	item.Details = sanitizeDiagnosticText(item.Details, s.policy.MaxDetailsBytes)
	return item, nil
}

func (s *typedSanitizer) FindingPage(page finding.Page) (finding.Page, error) {
	if len(page.Items) > finding.MaxLimit {
		return finding.Page{}, ErrPageTooLarge
	}
	if page.Pagination.Limit < 0 || page.Pagination.Limit > finding.MaxLimit {
		return finding.Page{}, fmt.Errorf("%w: pagination.limit", ErrUnsafeField)
	}
	if err := validateBoundedField("pagination.continue", page.Pagination.Continue, s.policy.MaxCursorBytes, false); err != nil {
		return finding.Page{}, err
	}

	items := make([]finding.Finding, len(page.Items))
	totalDiagnosticBytes := 0
	for i, item := range page.Items {
		safe, err := s.Finding(item)
		if err != nil {
			return finding.Page{}, err
		}
		totalDiagnosticBytes += len(safe.Problem) + len(safe.Details)
		if totalDiagnosticBytes > s.policy.MaxPageDiagnosticBytes {
			return finding.Page{}, ErrPageTooLarge
		}
		items[i] = safe
	}
	page.Items = items
	return page, nil
}

func (s *typedSanitizer) FindingSummary(summary finding.Summary) (finding.Summary, error) {
	if summary.Total < 0 {
		return finding.Summary{}, fmt.Errorf("%w: summary.total", ErrUnsafeField)
	}
	keyCount := len(summary.BySeverity) + len(summary.ByKind) + len(summary.ByNamespace)
	if keyCount > s.policy.MaxSummaryKeys {
		return finding.Summary{}, ErrSummaryTooLarge
	}
	for key, count := range summary.BySeverity {
		if count < 0 {
			return finding.Summary{}, fmt.Errorf("%w: summary.bySeverity count", ErrUnsafeField)
		}
		switch key {
		case finding.SeverityCritical, finding.SeverityWarning, finding.SeverityInfo:
		default:
			return finding.Summary{}, fmt.Errorf("%w: summary.bySeverity key", ErrUnsafeField)
		}
	}
	if err := validateCountMap("summary.byKind", summary.ByKind, s.policy.MaxKindBytes); err != nil {
		return finding.Summary{}, err
	}
	if err := validateCountMap("summary.byNamespace", summary.ByNamespace, s.policy.MaxIdentifierBytes); err != nil {
		return finding.Summary{}, err
	}
	return summary, nil
}

func (s *typedSanitizer) validateResourceRef(ref finding.ResourceRef) error {
	if err := validateBoundedField("resource.apiVersion", ref.APIVersion, s.policy.MaxAPIVersionBytes, false); err != nil {
		return err
	}
	if err := validateBoundedField("resource.kind", ref.Kind, s.policy.MaxKindBytes, false); err != nil {
		return err
	}
	if err := validateBoundedField("resource.namespace", ref.Namespace, s.policy.MaxIdentifierBytes, false); err != nil {
		return err
	}
	return validateBoundedField("resource.name", ref.Name, s.policy.MaxIdentifierBytes, false)
}

func validateCountMap(name string, values map[string]int, maxKeyBytes int) error {
	for key, count := range values {
		if count < 0 {
			return fmt.Errorf("%w: %s count", ErrUnsafeField, name)
		}
		if err := validateBoundedField(name+" key", key, maxKeyBytes, true); err != nil {
			return err
		}
	}
	return nil
}

func validateBoundedField(name, value string, maxBytes int, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%w: %s is required", ErrUnsafeField, name)
		}
		return nil
	}
	if !utf8.ValidString(value) || len(value) > maxBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s", ErrUnsafeField, name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s", ErrUnsafeField, name)
		}
	}
	return nil
}
