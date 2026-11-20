package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/security"
	"golang.org/x/crypto/bcrypt"
)

func TestPortalWikiAskUsesScopedEvidenceAndModel(t *testing.T) {
	const origin = "https://portal.example.com"
	t.Setenv("AUTH_ORIGIN", origin)
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "false")

	store, err := identitystore.Open(filepath.Join(t.TempDir(), "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const password = "fixture-portal-user"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(t.Context(), "INSERT INTO local_accounts VALUES (?, ?, ?)", "operator", string(hash), "[]"); err != nil {
		t.Fatal(err)
	}
	policy := []byte(`{"version":"v1","rules":[{"effect":"allow","subjects":["local:operator"],"capabilities":["wiki:read"],"scopes":[{"cluster":"local","namespace":"dev"}]}]}`)
	if err := store.Import(t.Context(), nil, policy); err != nil {
		t.Fatal(err)
	}
	for _, doc := range []struct{ id, namespace, body string }{
		{"dev-guide", "dev", "ImagePullBackOff: 检查允许的镜像仓库"},
		{"prod-guide", "prod", "ImagePullBackOff: 仅生产可读的内容"},
	} {
		if _, err := store.ExecContext(t.Context(), "INSERT INTO wiki_documents VALUES (?, 'local', ?, 'test', 'ops', '排障手册', '镜像拉取手册', ?, '2026-11-17T00:00:00Z')", doc.id, doc.namespace, doc.body); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ExecContext(t.Context(), "INSERT INTO wiki_publications VALUES(?,0,?)", doc.id, `{"reviewedAt":"2026-11-17"}`); err != nil {
			t.Fatal(err)
		}
	}

	modelRequests := make(chan string, 1)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string                     `json:"model"`
			Messages []struct{ Content string } `json:"messages"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" ||
			r.Header.Get("Authorization") != "Bearer fixture-model-key" ||
			json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "fixture" || len(request.Messages) != 2 {
			http.Error(w, "unexpected model request", http.StatusBadRequest)
			return
		}
		select {
		case modelRequests <- request.Messages[1].Content:
		default:
			http.Error(w, "unexpected duplicate model request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"answer\":\"请检查镜像仓库。\",\"citations\":[\"K1\",\"invented\"]}"}}]}`))
	}))
	defer model.Close()

	review, err := diagnosis.New(testBackend{}, store, func(context.Context) (kubernetes.EngineAIConfig, error) {
		return kubernetes.EngineAIConfig{Endpoint: model.URL + "/chat/completions", Model: "fixture", Key: "fixture-model-key"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := defaultProductionBundle(testLogger())
	wrap, err := configureIdentityHandler(t.Context(), &bundle, store, review, nil)
	if err != nil {
		t.Fatal(err)
	}
	api, err := buildHandler(testLogger(), testBackend{}, 0, security.ModeProduction, bundle)
	if err != nil {
		t.Fatal(err)
	}
	handler := wrap(api)
	request := func(method, path, requestOrigin, body string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", requestOrigin)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, recorder.Code, want, recorder.Body)
		}
		return recorder
	}
	const ask = "/api/v1/wiki/ask"
	const question = `{"question":"ImagePullBackOff"}`
	request(http.MethodPost, ask, origin, question, nil, http.StatusUnauthorized)
	login := request(http.MethodPost, "/api/v1/auth/login", origin,
		`{"provider":"local","username":"operator","password":"`+password+`"}`, nil, http.StatusNoContent)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected a session cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	request(http.MethodPost, ask, "https://other.example.com", question, cookie, http.StatusForbidden)
	request(http.MethodGet, "/api/v1/wiki/knowledge/prod-guide/revisions/1", origin, "", cookie, http.StatusNotFound)

	response := request(http.MethodPost, ask, origin, question, cookie, http.StatusOK)
	var answer struct {
		Answer    string `json:"answer"`
		Citations []struct {
			ID string `json:"knowledgeId"`
		} `json:"citations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.Answer != "请检查镜像仓库。" ||
		len(answer.Citations) != 1 || answer.Citations[0].ID != "dev-guide" {
		t.Fatalf("unexpected answer: %s", response.Body)
	}
	select {
	case prompt := <-modelRequests:
		if !strings.Contains(prompt, "检查允许的镜像仓库") || strings.Contains(prompt, "仅生产可读") {
			t.Fatalf("unexpected scoped evidence in model request: %s", prompt)
		}
	default:
		t.Fatal("Wiki question never reached the model")
	}
}
