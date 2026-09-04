package sanitizer

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

func TestFindingRedactsCredentialsAndActiveContent(t *testing.T) {
	s := Default()
	secret := "test-credential-value"
	jwt := "eyJ" + strings.Repeat("a", 8) + "." +
		"eyJ" + strings.Repeat("b", 8) + "." +
		strings.Repeat("c", 24)
	item := validFinding()
	item.Problem = "Authorization: Bearer " + secret + "\nCookie: sid=" + secret
	item.Details = "password=" + secret + " token=" + jwt + ` <script>alert(1)</script> <img src=x onerror="alert(2)"> javascript:alert(3)`

	safe, err := s.Finding(item)
	if err != nil {
		t.Fatalf("sanitize finding: %v", err)
	}
	joined := safe.Problem + safe.Details
	if strings.Contains(joined, secret) || strings.Contains(joined, jwt) {
		t.Fatalf("credential leaked: %q", joined)
	}
	for _, marker := range []string{credentialMarker, headerMarker, activeContentMarker} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("expected marker %q in %q", marker, joined)
		}
	}
	if strings.Contains(strings.ToLower(joined), "<script") || strings.Contains(strings.ToLower(joined), "onerror=") || strings.Contains(strings.ToLower(joined), "javascript:") {
		t.Fatalf("active content was not neutralized: %q", joined)
	}
}

func TestFindingFalsePositivesPreservedAndIdempotent(t *testing.T) {
	s := Default()
	item := validFinding()
	item.Namespace = "secret-rotation"
	item.Resource.Namespace = "secret-rotation"
	item.Resource.Name = "token-bucket-worker"
	item.Problem = "token bucket is throttling requests"
	item.Details = "image digest sha256:0123456789abcdef and secret-rotation namespace"

	first, err := s.Finding(item)
	if err != nil {
		t.Fatalf("sanitize finding: %v", err)
	}
	second, err := s.Finding(first)
	if err != nil {
		t.Fatalf("sanitize finding twice: %v", err)
	}
	if first != second {
		t.Fatalf("sanitizer is not idempotent: first=%#v second=%#v", first, second)
	}
	if first.Namespace != item.Namespace || first.Resource.Name != item.Resource.Name || first.Problem != item.Problem || first.Details != item.Details {
		t.Fatalf("false positive changed safe fields: %#v", first)
	}
}

func TestDiagnosticBoundsPreserveUTF8(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxProblemBytes = 32
	policy.MaxDetailsBytes = 40
	policy.MaxPageDiagnosticBytes = 128
	s, err := New(policy)
	if err != nil {
		t.Fatalf("new sanitizer: %v", err)
	}
	item := validFinding()
	item.Problem = strings.Repeat("界", 20)
	item.Details = strings.Repeat("测", 20)

	safe, err := s.Finding(item)
	if err != nil {
		t.Fatalf("sanitize finding: %v", err)
	}
	if len(safe.Problem) > policy.MaxProblemBytes || len(safe.Details) > policy.MaxDetailsBytes {
		t.Fatalf("bounds exceeded: problem=%d details=%d", len(safe.Problem), len(safe.Details))
	}
	if !utf8.ValidString(safe.Problem) || !utf8.ValidString(safe.Details) {
		t.Fatalf("truncation split UTF-8")
	}
	if !strings.HasSuffix(safe.Problem, truncationMarker) || !strings.HasSuffix(safe.Details, truncationMarker) {
		t.Fatalf("expected deterministic truncation markers")
	}
}

func TestFindingRejectsUnsafeMetadata(t *testing.T) {
	s := Default()
	item := validFinding()
	item.Namespace = "prod\nadmin"
	if _, err := s.Finding(item); !errors.Is(err, ErrUnsafeField) {
		t.Fatalf("expected unsafe field error, got %v", err)
	}
}

func TestFindingPageAggregateBound(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxProblemBytes = 32
	policy.MaxDetailsBytes = 32
	policy.MaxPageDiagnosticBytes = 70
	s, err := New(policy)
	if err != nil {
		t.Fatalf("new sanitizer: %v", err)
	}
	item := validFinding()
	item.Problem = strings.Repeat("a", 32)
	item.Details = strings.Repeat("b", 32)
	page := finding.Page{Items: []finding.Finding{item, item}, Pagination: finding.Pagination{Limit: 2}}
	if _, err := s.FindingPage(page); !errors.Is(err, ErrPageTooLarge) {
		t.Fatalf("expected page bound error, got %v", err)
	}
}

func TestSummaryAndResourceValidation(t *testing.T) {
	s := Default()
	summary := finding.Summary{
		Total:       1,
		BySeverity:  map[string]int{finding.SeverityCritical: 0, finding.SeverityWarning: 1, finding.SeverityInfo: 0},
		ByKind:      map[string]int{"Deployment": 1},
		ByNamespace: map[string]int{"prod": 1},
	}
	if _, err := s.FindingSummary(summary); err != nil {
		t.Fatalf("sanitize summary: %v", err)
	}

	resource := kubernetes.ResourceDetail{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "api"}
	if _, err := s.Resource(resource); err != nil {
		t.Fatalf("sanitize resource: %v", err)
	}
	resource.Name = "api\x00shadow"
	if _, err := s.Resource(resource); !errors.Is(err, ErrUnsafeField) {
		t.Fatalf("expected unsafe resource field error, got %v", err)
	}
}

func validFinding() finding.Finding {
	return finding.Finding{
		ID:        "finding-1",
		Cluster:   "local",
		Namespace: "prod",
		Severity:  finding.SeverityWarning,
		Resource:  finding.ResourceRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "api"},
		Problem:   "deployment is unavailable",
		Details:   "ready replicas are below desired replicas",
		Source:    "k8sgpt",
		CreatedAt: "2026-09-04T00:00:00Z",
	}
}
