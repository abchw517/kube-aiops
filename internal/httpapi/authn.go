package httpapi

import (
	"errors"
	"net/http"
	"strings"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
)

type HandlerOptions struct {
	// Authenticator activates authentication enforcement for /api/v1/* when non-nil.
	// Phase 1.4 intentionally does not provide an insecure built-in credential mechanism.
	Authenticator identity.Authenticator
	// Authorizer narrows the authenticated read-only API surface using application-level capability
	// and cluster/namespace scope decisions. When authentication is active and Authorizer is nil,
	// protected routes fail closed instead of silently bypassing authorization.
	Authorizer authorization.Authorizer
	// AuditSink activates the provider-neutral Phase 1.4.3 audit pipeline for known protected routes.
	// The sink receives only validated bounded events and cannot alter AuthN/AuthZ decisions.
	AuditSink internalaudit.Sink
	// Sanitizer optionally replaces the default Phase 1.4.4 typed response sanitizer, primarily for
	// deterministic tests. A nil value never disables sanitization; NewHandlerWithOptions installs
	// the immutable default policy instead.
	Sanitizer sanitizer.Sanitizer
}

func (s *Server) authenticationMiddleware(authenticator identity.Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresAuthentication(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		principal, err := authenticator.Authenticate(r.Context(), r)
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrUnauthenticated):
				setAuditOutcome(r, internalaudit.OutcomeUnauthenticated)
				writeAuthenticationRequired(w)
			default:
				setAuditOutcome(r, internalaudit.OutcomeSecurityUnavailable)
				metadata := requestMetadataFromContext(r.Context())
				s.logger.Warn(
					"authentication provider unavailable",
					"reason", "provider_error",
					"request_id", metadata.RequestID,
					"correlation_id", metadata.CorrelationID,
				)
				writeAuthenticationUnavailable(w)
			}
			return
		}

		if err := principal.Validate(); err != nil {
			setAuditOutcome(r, internalaudit.OutcomeSecurityUnavailable)
			metadata := requestMetadataFromContext(r.Context())
			s.logger.Warn(
				"authentication provider returned invalid principal",
				"reason", "invalid_principal",
				"request_id", metadata.RequestID,
				"correlation_id", metadata.CorrelationID,
			)
			writeAuthenticationUnavailable(w)
			return
		}

		if recorder, ok := internalaudit.RecorderFromContext(r.Context()); ok {
			recorder.SetPrincipal(principal.Subject, principal.Provider)
		}
		ctx := identity.WithPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func setAuditOutcome(r *http.Request, outcome internalaudit.Outcome) {
	if recorder, ok := internalaudit.RecorderFromContext(r.Context()); ok {
		recorder.SetOutcome(outcome)
	}
}

func requiresAuthentication(path string) bool {
	return path == "/api/v1" || strings.HasPrefix(path, "/api/v1/")
}

func writeAuthenticationRequired(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication required")
}

func writeAuthenticationUnavailable(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "authentication service unavailable")
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusForbidden, "AUTHORIZATION_DENIED", "access denied")
}
