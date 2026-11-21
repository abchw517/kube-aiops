package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
)

type incidentProductionBackend struct{ assignmentBackend }

func (incidentProductionBackend) Clusters() []kubernetes.Cluster {
	return []kubernetes.Cluster{{ID: "local", Name: "local", Environment: "production", Status: "ready"}}
}

func TestIncidentPortalKeepsBusinessGrantsSeparateInSharedNamespace(t *testing.T) {
	const origin = "https://portal.example.test"
	t.Setenv("AUTH_ORIGIN", origin)
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "false")
	t.Setenv("DIAGNOSTIC_INCIDENT_ENVIRONMENTS", "production")
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "incident-portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Import(ctx, nil, []byte(`{"version":"v1","rules":[]}`)); err != nil {
		t.Fatal(err)
	}
	registry := &httpapi.ClusterBackends{Backend: incidentProductionBackend{}}
	managed, err := newOnboarding(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(ctx, `INSERT INTO resource_catalog_ids VALUES
		('business-lines','fixture-a','fixture-a','fixture-a'),('business-lines','fixture-b','fixture-b','fixture-b');
		INSERT INTO namespace_business VALUES ('local','payments','fixture-a'),('local','payments','fixture-b')`); err != nil {
		t.Fatal(err)
	}

	state := `{"accounts":{"ldap:fixture-a":{"id":"ldap:fixture-a","username":"fixture-a","kind":"ldap","generation":"v1","roles":["fixture-a"]},
		"ldap:fixture-b":{"id":"ldap:fixture-b","username":"fixture-b","kind":"ldap","generation":"v1","roles":["fixture-b"]}},
		"roles":{"fixture-a":ROLE_A,"fixture-b":ROLE_B},"groups":{}}`
	role := func(business string) string {
		grants := make([]map[string]any, 0, 3)
		scope := map[string]string{"environment": "production", "cluster": "local", "namespace": "payments", "business": business}
		for _, code := range []string{incident.ListPermission, incident.CreatePermission, incident.ReadPermission} {
			grants = append(grants, map[string]any{"code": code, "scope": scope})
		}
		value, err := json.Marshal(map[string]any{"id": business, "name": business, "grants": grants})
		if err != nil {
			t.Fatal(err)
		}
		return string(value)
	}
	state = strings.NewReplacer("ROLE_A", role("fixture-a"), "ROLE_B", role("fixture-b")).Replace(state)
	if _, err := store.ExecContext(ctx, "UPDATE platform_identity SET document=? WHERE id=1", state); err != nil {
		t.Fatal(err)
	}

	admin, err := permissions.New(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	admin.SetScopeResolvers(managed.clusterEnvironment, managed.namespaceBusiness)
	sessions, err := identity.NewSessions(origin, map[string]identity.PasswordVerifier{"ldap": func(_ context.Context, user, _ string) (identity.Principal, error) {
		return identity.Principal{Subject: "ldap:" + user, Provider: "platform-ldap", SessionVersion: "v1"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	sessions.ValidateSession = admin.ValidateSession
	server, err := configureIncidents(ctx, store, admin, sessions, managed, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := sessions.Wrap(wrapAdmin(admin, wiki.New(store, admin, origin), sessions, http.NotFoundHandler(), nil, managed, server))

	call := func(method, path, body string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "portal-fixture")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	login := func(user string) *http.Cookie {
		return call("POST", "/api/v1/auth/login", `{"provider":"ldap","username":"`+user+`","password":"fixture-password"}`, nil, 204).Result().Cookies()[0]
	}

	const root = "/api/v1/diagnostics"
	missing := call("GET", root+"/incidents", "", nil, 401)
	if missing.Header().Get("X-Request-ID") == "" || missing.Header().Get("X-Correlation-ID") == "" {
		t.Fatal("missing request metadata headers")
	}

	cookieA, cookieB := login("fixture-a"), login("fixture-b")
	body := func(business string) string {
		return `{"title":"Shared namespace incident","severity":"P2","scope":{"clusterId":"local","namespace":"payments","businessId":"` + business + `"}}`
	}
	call("POST", root+"/incidents", body("fixture-b"), cookieA, 403)
	createdA := call("POST", root+"/incidents", body("fixture-a"), cookieA, 201)
	createdB := call("POST", root+"/incidents", body("fixture-b"), cookieB, 201)
	call("GET", createdA.Header().Get("Location"), "", cookieA, 200)
	call("GET", createdB.Header().Get("Location"), "", cookieA, 404)
	list := call("GET", root+"/incidents", "", cookieA, 200)
	if !strings.Contains(list.Body.String(), "fixture-a") || strings.Contains(list.Body.String(), "fixture-b") {
		t.Fatalf("business-specific role saw wrong incident: %s", list.Body)
	}
}
