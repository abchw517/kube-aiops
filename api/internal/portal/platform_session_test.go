package portal

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/security"
)

func TestPlatformGuestPasswordResetRevokesExistingSessions(t *testing.T) {
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "true")
	t.Setenv("AUTH_ORIGIN", "https://portal.example")
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bundle := defaultProductionBundle(testLogger())
	wrap, err := configureIdentityHandler(t.Context(), &bundle, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	api, err := buildHandler(testLogger(), testBackend{}, 0, security.ModeProduction, bundle)
	if err != nil {
		t.Fatal(err)
	}
	handler := wrap(api)
	call := func(method, path, data string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://portal.example")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d %s", method, path, response.Code, want, response.Body)
		}
		return response
	}
	login := func(name, password string) *http.Cookie {
		t.Helper()
		return call("POST", "/api/v1/auth/login", `{"provider":"local","username":"`+name+`","password":"`+password+`"}`, nil, 204).Result().Cookies()[0]
	}
	const rootPassword = "fixture-root-verified"
	const guestTemporary = "fixture-guest-temporary"
	const guestPassword = "fixture-guest-changed"
	const nextTemporary = "fixture-guest-reset"
	initial := login("admin", "admin123")
	call("POST", "/api/v1/admin/password", `{"current":"admin123","password":"`+rootPassword+`"}`, initial, 204)
	admin := login("admin", rootPassword)
	const auditRetention = `{"low":0,"medium":5,"high":7}`
	call("PUT", "/api/v1/admin/operations", auditRetention, nil, 401)
	call("PUT", "/api/v1/admin/operations", auditRetention, admin, 204)
	auditPolicy := call("GET", "/api/v1/admin/operations", "", admin, 200)
	if !strings.Contains(auditPolicy.Body.String(), `"retention":{"low":0,"medium":5,"high":7}`) {
		t.Fatal(auditPolicy.Body.String())
	}
	call("POST", "/api/v1/admin/accounts", `{"action":"create","name":"reader","kind":"guest","password":"`+guestTemporary+`"}`, admin, 200)
	first := login("reader", guestTemporary)
	status := call("GET", "/api/v1/admin/status", "", first, 200)
	if !strings.Contains(status.Body.String(), `"mustChangePassword":true`) {
		t.Fatal(status.Body.String())
	}
	call("GET", "/api/v1/admin/accounts", "", first, 403)
	call("POST", "/api/v1/admin/password", `{"current":"`+guestTemporary+`","password":"`+guestPassword+`"}`, first, 204)
	call("GET", "/api/v1/admin/status", "", first, 401)
	regular := login("reader", guestPassword)
	another := login("reader", guestPassword)
	call("POST", "/api/v1/admin/accounts", `{"action":"reset","id":"local:reader","password":"`+nextTemporary+`"}`, admin, 200)
	call("GET", "/api/v1/admin/status", "", regular, 401)
	call("GET", "/api/v1/admin/status", "", another, 401)
	audit := call("GET", "/api/v1/admin/operations?risk=高", "", admin, 200)
	if !strings.Contains(audit.Body.String(), "重置密码") || strings.Contains(audit.Body.String(), nextTemporary) {
		t.Fatal(audit.Body.String())
	}
}
