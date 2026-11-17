package security

import (
	"fmt"
	"reflect"

	internalaudit "github.com/abchw517/kube-aiops/api/internal/audit"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

type Bundle struct {
	Authenticator identity.Authenticator
	Authorizer    authorization.Authorizer
	AuditSink     internalaudit.Sink
	Sanitizer     sanitizer.Sanitizer
}

type ValidationError struct {
	Component string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("production security bundle missing required component: %s", e.Component)
}

func (b Bundle) ValidateForProduction() error {
	checks := []struct {
		name  string
		value any
	}{
		{name: "authenticator", value: b.Authenticator},
		{name: "authorizer", value: b.Authorizer},
		{name: "audit_sink", value: b.AuditSink},
		{name: "sanitizer", value: b.Sanitizer},
	}
	for _, check := range checks {
		if isNilComponent(check.value) {
			return &ValidationError{Component: check.name}
		}
	}
	return nil
}

func isNilComponent(value any) bool {
	if value == nil {
		return true
	}
	ref := reflect.ValueOf(value)
	switch ref.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return ref.IsNil()
	default:
		return false
	}
}
