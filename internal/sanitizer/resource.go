package sanitizer

import (
	"fmt"

	"github.com/abchw517/kube-aiops/internal/kubernetes"
)

func (s *typedSanitizer) Resource(resource kubernetes.ResourceDetail) (kubernetes.ResourceDetail, error) {
	if err := validateBoundedField("resource.apiVersion", resource.APIVersion, s.policy.MaxAPIVersionBytes, true); err != nil {
		return kubernetes.ResourceDetail{}, err
	}
	if err := validateBoundedField("resource.kind", resource.Kind, s.policy.MaxKindBytes, true); err != nil {
		return kubernetes.ResourceDetail{}, err
	}
	if err := validateBoundedField("resource.namespace", resource.Namespace, s.policy.MaxIdentifierBytes, true); err != nil {
		return kubernetes.ResourceDetail{}, err
	}
	if err := validateBoundedField("resource.name", resource.Name, s.policy.MaxIdentifierBytes, true); err != nil {
		return kubernetes.ResourceDetail{}, err
	}
	if err := validateBoundedField("resource.createdAt", resource.CreatedAt, s.policy.MaxCreatedAtBytes, false); err != nil {
		return kubernetes.ResourceDetail{}, err
	}
	if err := validateBoundedField("resource.status.phase", resource.Status.Phase, s.policy.MaxStatusBytes, false); err != nil {
		return kubernetes.ResourceDetail{}, err
	}
	for name, value := range map[string]*int64{
		"replicas":          resource.Status.Replicas,
		"readyReplicas":     resource.Status.ReadyReplicas,
		"availableReplicas": resource.Status.AvailableReplicas,
	} {
		if value != nil && *value < 0 {
			return kubernetes.ResourceDetail{}, fmt.Errorf("%w: resource.status.%s", ErrUnsafeField, name)
		}
	}
	return resource, nil
}
