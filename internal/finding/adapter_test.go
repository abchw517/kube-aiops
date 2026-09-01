package finding

import (
	"testing"

	"github.com/abchw517/kube-aiops/internal/k8sgpt"
)

func TestFromResultPrefersTargetRef(t *testing.T) {
	item := FromResult(k8sgpt.Result{
		Metadata: k8sgpt.Metadata{
			Name:              "finding-1",
			CreationTimestamp: "2026-09-01T00:00:00Z",
		},
		Spec: k8sgpt.ResultSpec{
			Kind:    "Pod",
			Name:    "legacy/legacy-pod",
			Details: " details ",
			Error:   []k8sgpt.Failure{{Text: " CrashLoopBackOff "}},
			TargetRef: &k8sgpt.TargetReference{
				APIVersion: "v1",
				Kind:       "Pod",
				Namespace:  "prod",
				Name:       "demo",
			},
		},
	})

	if item.Resource.Namespace != "prod" || item.Resource.Name != "demo" {
		t.Fatalf("targetRef not preferred: %#v", item.Resource)
	}
	if item.Problem != "CrashLoopBackOff" || item.Details != "details" || item.Severity != SeverityWarning {
		t.Fatalf("unexpected finding: %#v", item)
	}
}

func TestFromResultLegacyFallback(t *testing.T) {
	item := FromResult(k8sgpt.Result{
		Metadata: k8sgpt.Metadata{Name: "finding-2"},
		Spec:     k8sgpt.ResultSpec{Kind: "Deployment", Name: "dev/app"},
	})
	if item.Resource.Namespace != "dev" || item.Resource.Name != "app" || item.Resource.Kind != "Deployment" {
		t.Fatalf("unexpected legacy mapping: %#v", item.Resource)
	}
}

func TestMatches(t *testing.T) {
	item := Finding{
		Cluster:   "local",
		Namespace: "dev",
		Severity:  SeverityWarning,
		Resource:  ResourceRef{Kind: "Pod"},
		Problem:   "CrashLoopBackOff",
	}
	if !Matches(item, Filter{Cluster: "LOCAL", Namespace: "dev", Kind: "pod", Severity: "WARNING", Problem: "crashloop"}) {
		t.Fatal("expected finding to match filter")
	}
	if Matches(item, Filter{Namespace: "prod"}) {
		t.Fatal("unexpected namespace match")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	item := Finding{ID: "finding-1", CreatedAt: "2026-09-01T00:00:00Z"}
	token := EncodeCursor(item)
	cursor, err := DecodeCursor(token)
	if err != nil || cursor == nil || cursor.ID != item.ID || cursor.CreatedAt != item.CreatedAt {
		t.Fatalf("unexpected cursor result cursor=%#v err=%v", cursor, err)
	}
	if _, err := DecodeCursor("bad-token"); err == nil {
		t.Fatal("invalid cursor must fail")
	}
}

func TestNormalizeLimit(t *testing.T) {
	limit, err := NormalizeLimit(0)
	if err != nil || limit != DefaultLimit {
		t.Fatalf("unexpected default limit: %d err=%v", limit, err)
	}
	if _, err := NormalizeLimit(MaxLimit + 1); err == nil {
		t.Fatal("limit over maximum must fail")
	}
}
