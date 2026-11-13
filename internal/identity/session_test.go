package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionLoginAuthorizationAndLogout(t *testing.T) {
	s, err := NewSessions("https://portal.example.com", map[string]PasswordVerifier{"local": func(_ context.Context, user, pass string) (Principal, error) {
		if user != "alice" || pass != "correct" {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{Subject: "local:alice", Provider: "local"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.Authenticate(r.Context(), r); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	request := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/v1/findings", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	body := `{"provider":"local","username":"alice","password":"correct"}`
	if w := request("POST", "/api/v1/auth/login", body, "https://evil.example", nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/api/v1/auth/login", body, "https://portal.example.com", nil)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login: %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie")
	}
	if w := request("GET", "/api/v1/findings", "", "", cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/api/v1/auth/logout", "", "https://portal.example.com", cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/api/v1/findings", "", "", cookie); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
