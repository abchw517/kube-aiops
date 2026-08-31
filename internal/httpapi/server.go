package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type ReadinessChecker interface {
	Ready(context.Context) error
}

type Server struct {
	logger       *slog.Logger
	readiness    ReadinessChecker
	readyTimeout time.Duration
}

func NewHandler(logger *slog.Logger, readiness ReadinessChecker, readyTimeout time.Duration) http.Handler {
	server := &Server{
		logger:       logger,
		readiness:    readiness,
		readyTimeout: readyTimeout,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.healthz)
	mux.HandleFunc("GET /readyz", server.readyz)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.readyTimeout)
	defer cancel()

	if err := s.readiness.Ready(ctx); err != nil {
		s.logger.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
