package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
)

type HandlerOptions struct {
	// Authenticator activates authentication enforcement for /api/v1/* when non-nil.
	// Phase 1.4 intentionally does not provide an insecure built-in credential mechanism.
	Authenticator identity.Authenticator
	// Authorizer narrows the authenticated read-only API surface using application-level capability
	// and cluster/namespace scope decisions. When authentication is active and Authorizer is nil,
	// protected routes fail closed instead of silently bypassing authorization.
	Authorizer authorization.Authorizer
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
				writeAuthenticationRequired(w)
			default:
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

		ctx := identity.WithPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
