package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "__Host-kube-aiops-session"
const sessionLifetime = 30 * time.Minute

type PasswordVerifier func(context.Context, string, string) (Principal, error)

type browserSession struct {
	principal Principal
	expires   time.Time
}

type Sessions struct {
	origin    string
	providers map[string]PasswordVerifier
	mu        sync.Mutex
	active    map[[32]byte]browserSession
	attempts  float64
	refillAt  time.Time
	workers   chan struct{}
}

func NewSessions(origin string, providers map[string]PasswordVerifier) (*Sessions, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || origin != "https://"+u.Host || len(providers) == 0 {
		return nil, errors.New("HTTPS AUTH_ORIGIN and at least one login provider are required")
	}
	return &Sessions{
		origin: origin, providers: providers, active: make(map[[32]byte]browserSession),
		attempts: 20, refillAt: time.Now(), workers: make(chan struct{}, 4),
	}, nil
}

func (s *Sessions) Authenticate(_ context.Context, r *http.Request) (Principal, error) {
	key, ok := sessionKey(r)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.active[key]
	if !ok || !time.Now().Before(session.expires) {
		delete(s.active, key)
		return Principal{}, ErrUnauthenticated
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

func (s *Sessions) allowAttempt() bool {
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
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
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
	verify := s.providers[credentials.Provider]
	if verify == nil || validateRequiredValue("username", credentials.Username, maxSubjectLength) != nil ||
		strings.ContainsRune(credentials.Password, 0) || len(credentials.Password) == 0 || len(credentials.Password) > 1024 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	select {
	case s.workers <- struct{}{}:
		defer func() { <-s.workers }()
	default:
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if !s.allowAttempt() {
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

	var random [32]byte
	rand.Read(random[:])
	token := base64.RawURLEncoding.EncodeToString(random[:])
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
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
	expires := now.Add(sessionLifetime)
	s.active[sha256.Sum256([]byte(token))] = browserSession{principal, expires}
	writeSessionCookie(w, token, int(sessionLifetime.Seconds()))
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
