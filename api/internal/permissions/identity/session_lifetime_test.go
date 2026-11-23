package identity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSessionLifetimeReadsPolicyOnlyOnLogin 验证两类账号使用同一策略且已签发会话按原截止时间处理。
func TestSessionLifetimeReadsPolicyOnlyOnLogin(t *testing.T) {
	const origin = "https://portal.example.com"
	verify := func(_ context.Context, user, password string) (Principal, error) {
		if password != "correct" {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{Subject: user + ":alice", Provider: user}, nil
	}
	s, err := NewSessions(origin, map[string]PasswordVerifier{"local": verify, "ldap": verify})
	if err != nil {
		t.Fatal(err)
	}
	minutes, reads := 60, 0
	s.SessionLifetimeMinutes = func(context.Context) (int, error) {
		reads++
		return minutes, nil
	}
	handler := s.Wrap(http.NotFoundHandler())
	login := func(provider, password string, wantCode int) *http.Cookie {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
			`{"provider":"`+provider+`","username":"`+provider+`","password":"`+password+`"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != wantCode {
			t.Fatalf("login: got %d want %d", w.Code, wantCode)
		}
		cookies := w.Result().Cookies()
		if wantCode != http.StatusNoContent {
			if len(cookies) != 0 {
				t.Fatal("failed login issued a cookie")
			}
			return nil
		}
		if len(cookies) != 1 || cookies[0].MaxAge != minutes*60 {
			t.Fatalf("login: got cookies %v, want Max-Age=%d", cookies, minutes*60)
		}
		return cookies[0]
	}
	login("local", "wrong", http.StatusUnauthorized)
	if reads != 0 {
		t.Fatal("invalid credentials read the policy")
	}
	before := time.Now()
	oldCookie := login("local", "correct", http.StatusNoContent)
	after := time.Now()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/findings", nil)
	r.AddCookie(oldCookie)
	key, _ := sessionKey(r)
	oldExpiry := s.active[key].expires
	if oldExpiry.Before(before.Add(time.Hour)) || oldExpiry.After(after.Add(time.Hour)) {
		t.Fatalf("server expiry %v does not match 60 minute cookie", oldExpiry)
	}

	minutes = 5
	login("ldap", "correct", http.StatusNoContent)
	if _, err := s.Authenticate(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if reads != 2 || !s.active[key].expires.Equal(oldExpiry) {
		t.Fatal("authentication re-read the policy or changed the original expiry")
	}
	record := s.active[key]
	record.expires = time.Now()
	s.active[key] = record
	if _, err := s.Authenticate(t.Context(), r); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("authentication at the expiry boundary: %v", err)
	}
	s.SessionLifetimeMinutes = func(context.Context) (int, error) {
		return 0, errors.New("policy storage unavailable")
	}
	login("local", "correct", http.StatusServiceUnavailable)
}
