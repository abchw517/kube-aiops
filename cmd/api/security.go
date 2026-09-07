package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	internalaudit "github.com/abchw517/kube-aiops/internal/audit"
	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/httpapi"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
	"github.com/abchw517/kube-aiops/internal/security"
)

func buildHandler(
	logger *slog.Logger,
	backend httpapi.Backend,
	readyTimeout time.Duration,
	mode security.Mode,
	bundle security.Bundle,
	correlators ...correlation.Correlator,
) (http.Handler, error) {
	correlator := correlation.Disabled()
	if len(correlators) > 1 {
		return nil, fmt.Errorf("multiple correlators are not supported")
	}
	if len(correlators) == 1 && correlators[0] != nil {
		correlator = correlators[0]
	}

	switch mode {
	case security.ModeDevelopment:
		return httpapi.NewHandlerWithOptions(logger, backend, readyTimeout, httpapi.HandlerOptions{
			Correlator: correlator,
		}), nil
	case security.ModeProduction:
		if err := bundle.ValidateForProduction(); err != nil {
			return nil, err
		}
		return httpapi.NewHandlerWithOptions(logger, backend, readyTimeout, httpapi.HandlerOptions{
			Authenticator: bundle.Authenticator,
			Authorizer:    bundle.Authorizer,
			AuditSink:     bundle.AuditSink,
			Sanitizer:     bundle.Sanitizer,
			Correlator:    correlator,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported security mode %q", mode)
	}
}

func defaultProductionBundle(logger *slog.Logger) security.Bundle {
	return security.Bundle{
		AuditSink: internalaudit.NewSlogSink(logger),
		Sanitizer: sanitizer.Default(),
	}
}
