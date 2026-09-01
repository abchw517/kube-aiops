package finding

import (
	"strings"

	"github.com/abchw517/kube-aiops/internal/k8sgpt"
)

const (
	SourceK8sGPT = "k8sgpt"
	LocalCluster = "local"
)

func FromResult(result k8sgpt.Result) Finding {
	resource := ResourceRef{}
	if result.Spec.TargetRef != nil {
		resource = ResourceRef{
			APIVersion: strings.TrimSpace(result.Spec.TargetRef.APIVersion),
			Kind:       strings.TrimSpace(result.Spec.TargetRef.Kind),
			Namespace:  strings.TrimSpace(result.Spec.TargetRef.Namespace),
			Name:       strings.TrimSpace(result.Spec.TargetRef.Name),
		}
	}
	if resource.Kind == "" {
		resource.Kind = strings.TrimSpace(result.Spec.Kind)
	}
	if resource.Name == "" {
		legacyNS, legacyName := splitLegacyName(result.Spec.Name)
		resource.Name = legacyName
		if resource.Namespace == "" {
			resource.Namespace = legacyNS
		}
	}

	problem := firstNonEmptyError(result.Spec.Error)
	if problem == "" {
		problem = "Kubernetes resource finding"
	}

	return Finding{
		ID:        strings.TrimSpace(result.Metadata.Name),
		Cluster:   LocalCluster,
		Namespace: resource.Namespace,
		Severity:  SeverityWarning,
		Resource:  resource,
		Problem:   problem,
		Details:   strings.TrimSpace(result.Spec.Details),
		Source:    SourceK8sGPT,
		CreatedAt: strings.TrimSpace(result.Metadata.CreationTimestamp),
	}
}

func Matches(item Finding, filter Filter) bool {
	if value := strings.TrimSpace(filter.Cluster); value != "" && !strings.EqualFold(item.Cluster, value) {
		return false
	}
	if value := strings.TrimSpace(filter.Namespace); value != "" && item.Namespace != value {
		return false
	}
	if value := strings.TrimSpace(filter.Kind); value != "" && !strings.EqualFold(item.Resource.Kind, value) {
		return false
	}
	if value := strings.TrimSpace(filter.Severity); value != "" && !strings.EqualFold(item.Severity, value) {
		return false
	}
	if value := strings.TrimSpace(filter.Problem); value != "" && !strings.Contains(strings.ToLower(item.Problem), strings.ToLower(value)) {
		return false
	}
	return true
}

func splitLegacyName(value string) (string, string) {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, "/", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "", value
}

func firstNonEmptyError(items []k8sgpt.Failure) string {
	for _, item := range items {
		if value := strings.TrimSpace(item.Text); value != "" {
			return value
		}
	}
	return ""
}
