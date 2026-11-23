package portal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/security"
)

// TestSessionPolicyAdministration 验证真实路由、策略持久化和原子审计行为。
func TestSessionPolicyAdministration(t *testing.T) {
	const origin = "https://portal.example"
	const policy = "/api/v1/admin/session-policy"
	const password = "fixture-root-verified"
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "true")
	t.Setenv("AUTH_ORIGIN", origin)
	dbPath := filepath.Join(t.TempDir(), "identity.db")
	store, err := identitystore.Open(dbPath)
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
	call := func(method, path, body, requestOrigin string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", requestOrigin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	checkMinutes := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		var payload struct {
			Minutes int `json:"sessionLifetimeMinutes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || payload.Minutes != want {
			t.Fatalf("policy: got %s want %d minutes: %v", w.Body, want, err)
		}
	}
	login := func(pass string, wantAge int) *http.Cookie {
		t.Helper()
		result := call(http.MethodPost, "/api/v1/auth/login", `{"provider":"local","username":"admin","password":"`+pass+`"}`, origin, nil, 204)
		cookies := result.Result().Cookies()
		if len(cookies) != 1 || cookies[0].MaxAge != wantAge {
			t.Fatalf("login cookie: got %v want Max-Age=%d", cookies, wantAge)
		}
		return cookies[0]
	}

	call(http.MethodGet, policy, "", origin, nil, 401)
	initial := login("admin123", 1800)
	call(http.MethodGet, policy, "", origin, initial, 403)
	call(http.MethodPut, policy, `{"sessionLifetimeMinutes":60}`, origin, initial, 403)
	call(http.MethodPost, "/api/v1/admin/password", `{"current":"admin123","password":"`+password+`"}`, origin, initial, 204)
	admin := login(password, 1800)
	checkMinutes(call(http.MethodGet, policy, "", origin, admin, 200), 30)
	call(http.MethodPut, policy, `{"sessionLifetimeMinutes":60}`, "https://other.example", admin, 403)
	for _, invalid := range []string{`{}`, `{"sessionLifetimeMinutes":null}`, `{"sessionLifetimeMinutes":5.5}`, `{"sessionLifetimeMinutes":4}`, `{"sessionLifetimeMinutes":481}`} {
		call(http.MethodPut, policy, invalid, origin, admin, 400)
	}
	checkMinutes(call(http.MethodGet, policy, "", origin, admin, 200), 30)

	checkMinutes(call(http.MethodPut, policy, `{"sessionLifetimeMinutes":60}`, origin, admin, 200), 60)
	login(password, 3600)
	checkMinutes(call(http.MethodGet, policy, "", origin, admin, 200), 60)
	var action, risk, target string
	err = store.QueryRowContext(t.Context(), "SELECT action,risk,target FROM operation_audit WHERE actor='local:admin' AND action='修改登录有效期'").Scan(&action, &risk, &target)
	if err != nil || action != "修改登录有效期" || risk != "高" || target != "30 分钟 → 60 分钟" {
		t.Fatalf("audit: %q %q %q %v", action, risk, target, err)
	}

	if _, err := store.Exec(`CREATE TRIGGER fail_policy_audit BEFORE INSERT ON operation_audit
		WHEN NEW.action='修改登录有效期' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPut, policy, `{"sessionLifetimeMinutes":5}`, origin, admin, 503)
	checkMinutes(call(http.MethodGet, policy, "", origin, admin, 200), 60)
	if _, err := store.Exec("DROP TRIGGER fail_policy_audit"); err != nil {
		t.Fatal(err)
	}
	checkMinutes(call(http.MethodPut, policy, `{"sessionLifetimeMinutes":5}`, origin, admin, 200), 5)
	checkMinutes(call(http.MethodPut, policy, `{"sessionLifetimeMinutes":480}`, origin, admin, 200), 480)
	reopened, err := identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var minutes int
	if err := reopened.QueryRow("SELECT minutes FROM login_session_policy WHERE id=1").Scan(&minutes); err != nil || minutes != 480 {
		t.Fatalf("reopened policy: got %d want 480: %v", minutes, err)
	}
}
