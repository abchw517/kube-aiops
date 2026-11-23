package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "__Host-kube-aiops-session"
const (
	DefaultSessionLifetimeMinutes = 30
	MinSessionLifetimeMinutes     = 5
	MaxSessionLifetimeMinutes     = 480
)

// ValidSessionLifetimeMinutes 校验登录会话有效期是否在平台允许的范围内。
func ValidSessionLifetimeMinutes(minutes int) bool {
	return minutes >= MinSessionLifetimeMinutes && minutes <= MaxSessionLifetimeMinutes
}

type PasswordVerifier func(context.Context, string, string) (Principal, error)

type browserSession struct {
	principal Principal
	expires   time.Time
}

type Sessions struct {
	origin          string
	providers       map[string]PasswordVerifier
	mu              sync.Mutex
	active          map[[32]byte]browserSession
	limits          map[string]*loginLimit
	SessionLifetimeMinutes func(context.Context) (int, error)
	ValidateSession        func(context.Context, Principal) error
	RecordLogin             func(context.Context, string, string, int)
}

type loginLimit struct {
	mu       sync.Mutex
	attempts float64
	refillAt time.Time
	workers  chan struct{}
}

func NewSessions(origin string, providers map[string]PasswordVerifier) (*Sessions, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || origin != "https://"+u.Host || len(providers) == 0 {
		return nil, errors.New("HTTPS AUTH_ORIGIN and at least one login provider are required")
	}
	limits := make(map[string]*loginLimit, len(providers))
	for name := range providers {
		limits[name] = &loginLimit{attempts: 20, refillAt: time.Now(), workers: make(chan struct{}, 2)}
	}
	return &Sessions{
		origin: origin, providers: providers, active: make(map[[32]byte]browserSession),
		limits: limits,
	}, nil
}

func (s *Sessions) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	key, ok := sessionKey(r)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	s.mu.Lock()
	session, ok := s.active[key]
	if !ok || !time.Now().Before(session.expires) {
		delete(s.active, key)
		s.mu.Unlock()
		return Principal{}, ErrUnauthenticated
	}
	s.mu.Unlock()
	if s.ValidateSession != nil {
		if err := s.ValidateSession(ctx, session.principal); err != nil {
			return Principal{}, err
		}
	}
	return session.principal, nil
}

func sessionKey(r *http.Request) ([32]byte, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) != 43 {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(cookie.Value)), true
}

func (s *loginLimit) allowAttempt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.attempts = min(20, s.attempts+now.Sub(s.refillAt).Seconds())
	s.refillAt = now
	if s.attempts < 1 {
		return false
	}
	s.attempts--
	return true
}

// Wrap registers only session management routes. Diagnostic requests go through
// the existing production authentication, authorization and audit handlers.
func (s *Sessions) Wrap(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", auditSession("login", s.login))
	mux.HandleFunc("POST /api/v1/auth/logout", auditSession("logout", s.logout))
	mux.Handle("/", next)
	return mux
}

func (s *Sessions) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if origins := r.Header.Values("Origin"); len(origins) != 1 || origins[0] != s.origin {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	var credentials struct {
		Provider string `json:"provider"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil || decoder.Decode(new(any)) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if s.RecordLogin != nil && validateRequiredValue("username", credentials.Username, maxSubjectLength) == nil {
		defer func() {
			if audit, ok := w.(*sessionAuditWriter); ok {
				s.RecordLogin(r.Context(), credentials.Username, audit.subject, audit.status)
			}
		}()
	}
	verify := s.providers[credentials.Provider]
	if verify == nil || validateRequiredValue("username", credentials.Username, maxSubjectLength) != nil ||
		strings.ContainsRune(credentials.Password, 0) || len(credentials.Password) == 0 || len(credentials.Password) > 1024 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	limit := s.limits[credentials.Provider]
	select {
	case limit.workers <- struct{}{}:
		defer func() { <-limit.workers }()
	default:
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if !limit.allowAttempt() {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	principal, err := verify(r.Context(), credentials.Username, credentials.Password)
	if errors.Is(err, ErrUnauthenticated) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err != nil || principal.Validate() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	// 每次成功登录只读取一次策略，已签发会话继续使用原截止时间。
	minutes := DefaultSessionLifetimeMinutes
	if s.SessionLifetimeMinutes != nil {
		minutes, err = s.SessionLifetimeMinutes(r.Context())
		if err != nil || !ValidSessionLifetimeMinutes(minutes) {
			slog.ErrorContext(r.Context(), "cannot read login session lifetime", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	lifetime := time.Duration(minutes) * time.Minute

	var random [32]byte
	rand.Read(random[:])
	token := base64.RawURLEncoding.EncodeToString(random[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, session := range s.active {
		if !now.Before(session.expires) {
			delete(s.active, key)
		}
	}
	if key, ok := sessionKey(r); ok {
		delete(s.active, key)
	}
	if len(s.active) >= 4096 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	expires := now.Add(lifetime)
	s.active[sha256.Sum256([]byte(token))] = browserSession{principal, expires}
	writeSessionCookie(w, token, int(lifetime.Seconds()))
	recordSessionPrincipal(w, principal)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Sessions) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if origins := r.Header.Values("Origin"); len(origins) != 1 || origins[0] != s.origin {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if key, ok := sessionKey(r); ok {
		s.mu.Lock()
		if session, exists := s.active[key]; exists && time.Now().Before(session.expires) {
			recordSessionPrincipal(w, session.principal)
		}
		delete(s.active, key)
		s.mu.Unlock()
	}
	writeSessionCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func writeSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

type sessionAuditWriter struct {
	http.ResponseWriter
	status   int
	subject  string
	provider string
}

func recordSessionPrincipal(w http.ResponseWriter, principal Principal) {
	if audit, ok := w.(*sessionAuditWriter); ok {
		audit.subject = principal.Subject
		audit.provider = principal.Provider
	}
}

func (w *sessionAuditWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func auditSession(action string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		audit := &sessionAuditWriter{ResponseWriter: w}
		handler(audit, r)
		slog.InfoContext(r.Context(), "portal session audit", "action", action,
			"http_status", audit.status, "subject", audit.subject, "provider", audit.provider, "latency_ms", time.Since(start).Milliseconds())
	}
}

// RevokeSubject expires all sessions for a subject after a credential change.
func (s *Sessions) RevokeSubject(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, session := range s.active {
		if session.principal.Subject == subject {
			delete(s.active, key)
		}
	}
}
