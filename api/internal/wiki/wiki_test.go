package wiki

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestWikiWithPortalTestAccounts(t *testing.T) {
	t.Setenv("AUTH_BOOTSTRAP_ADMIN", "true")
	t.Setenv("AUTH_ORIGIN", "https://portal.example.com")
	writeIdentityTestFile(t, "WIKI_CREDENTIAL_ENCRYPTION_KEY_FILE", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))

	dbPath := filepath.Join(t.TempDir(), "identity.db")
	store, err := identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	admin, err := permissions.New(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := identity.NewSessions(admin.Origin(), map[string]identity.PasswordVerifier{"local": admin.Local})
	if err != nil {
		t.Fatal(err)
	}
	var remoteCalls atomic.Int32
	var syncFailure atomic.Bool
	var addSecondPage atomic.Bool
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "wiki-alice" || pass != "fixture-alice-wiki-secret" {
			w.WriteHeader(401)
			return
		}
		remoteCalls.Add(1)
		pages := []wikiRemotePage{{ID: "page-1", Title: "远端排障", Body: "检查远端仓库"}}
		if addSecondPage.Load() {
			second := wikiRemotePage{ID: "page-2", Title: "第二页", Body: "已修复"}
			if syncFailure.Load() {
				second.Body = ""
			}
			pages = append(pages, second)
		}
		adminhttp.WriteJSON(w, wikiRemoteDirectory{Pages: pages})
	}))
	defer remote.Close()
	service := New(store, admin, admin.Origin())
	service.wikiTransport = remote.Client().Transport
	service.SetTargetResolver(func(_ context.Context, cluster, namespace string) (Target, error) {
		return Target{Cluster: cluster, Namespace: namespace, Environment: "testing"}, nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/admin/{resource}", func(w http.ResponseWriter, r *http.Request) {
		admin.Handle(w, r, r.PathValue("resource"), sessions)
	})
	var revokeDuringAsk func()
	service.Register(mux, sessions, func(_ context.Context, _ string, data []byte) (string, error) {
		if !bytes.Contains(data, []byte("镜像拉取")) || bytes.Contains(data, []byte("fixture-password-value")) {
			t.Fatal("model input does not have the expected sanitized knowledge")
		}
		if revokeDuringAsk != nil {
			revokeDuringAsk()
		}
		return `{"answer":"请检查仓库凭据。","citations":["K1","unknown","K1"]}`, nil
	})
	handler := sessions.Wrap(mux)
	request := func(method, path, body string, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", admin.Origin())
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	login := func(username, password string) *http.Cookie {
		t.Helper()
		return request("POST", "/api/v1/auth/login", `{"provider":"local","username":"`+username+`","password":"`+password+`"}`, nil, 204).Result().Cookies()[0]
	}
	initial := login("admin", "admin123")
	request("POST", "/api/v1/admin/password", `{"current":"admin123","password":"fixture-admin-new"}`, initial, 204)
	root := login("admin", "fixture-admin-new")
	const source = "/api/v1/wiki/sources/ops"
	const credential = source + "/credential"
	request("PUT", source, `{"url":"`+remote.URL+`/ops","cluster":"local","namespace":"dev"}`, root, 204)
	request("PUT", source, `{"url":"https://wiki.example.com/changed","cluster":"local","namespace":"dev"}`, root, 409)
	request("POST", "/api/v1/admin/users", `{"username":"alice","password":"fixture-alice-pass","groups":["ops"]}`, root, 204)
	request("POST", "/api/v1/admin/users", `{"username":"bob","password":"fixture-bob-pass","groups":["ops"]}`, root, 204)
	alice := login("alice", "fixture-alice-pass")
	bob := login("bob", "fixture-bob-pass")
	request("GET", credential, "", nil, 401)
	request("GET", credential, "", alice, 404)
	request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[{"effect":"allow","groups":["local:ops"],"capabilities":["wiki:sync"],"scopes":[{"cluster":"local","namespace":"dev"}]}]}`, root, 204)
	const wikiPassword = "fixture-alice-wiki-secret"
	request("PUT", credential, `{"username":"wiki-alice","password":"`+wikiPassword+`"}`, alice, 204)
	request("POST", "/api/v1/auth/logout", "", alice, 204)
	request("GET", credential, "", alice, 401)
	alice = login("alice", "fixture-alice-pass")
	result := request("GET", credential, "", alice, 200)
	if strings.TrimSpace(result.Body.String()) != `{"username":"wiki-alice","configured":true}` || result.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected credential response: %s", result.Body)
	}
	if result := request("GET", credential, "", bob, 200); strings.TrimSpace(result.Body.String()) != `{"configured":false}` {
		t.Fatalf("Bob received another user's binding: %s", result.Body)
	}
	request("PUT", credential, `{"username":"wiki-bob","password":"fixture-bob-wiki-secret"}`, bob, 204)
	request("DELETE", credential, "", bob, 204)

	// A fresh database handle can decrypt Alice's binding for a future sync job.
	reopened, err := identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := &Service{store: reopened}
	aliceCredential, err := restarted.loadWikiCredential(t.Context(), "ops", identity.Principal{Provider: "local", Subject: "local:alice"})
	if err != nil || aliceCredential != (wikiCredential{Username: "wiki-alice", Password: wikiPassword}) {
		t.Fatal("saved credential did not survive database reopen")
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil || bytes.Contains(raw, []byte(wikiPassword)) {
		t.Fatal("database read failed or database contains the cleartext password")
	}
	t.Run("publishes sanitized wiki documents and isolates scope", func(t *testing.T) {
		const knowledge = "/api/v1/wiki/knowledge"
		const imports = "/api/v1/wiki/imports"
		request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[{"effect":"allow","subjects":["local:alice"],"capabilities":["wiki:read","wiki:publish"],"scopes":[{"cluster":"local","namespace":"dev"}]}]}`, root, 204)
		input := `{"document":{"cluster":"local","namespace":"dev","environment":"测试","business":"运维","body":"# 镜像拉取\npassword=fixture-password-value"},"publish":false}`
		preview := request("POST", imports, input, alice, 200)
		if strings.Contains(preview.Body.String(), "fixture-password-value") || !strings.Contains(preview.Body.String(), `"title":"镜像拉取"`) {
			t.Fatalf("invalid preview: %s", preview.Body)
		}
		if out := request("GET", knowledge, "", alice, 200); !strings.Contains(out.Body.String(), `"total":0`) {
			t.Fatal(out.Body.String())
		}
		publish := strings.Replace(input, "false", "true", 1)
		request("POST", imports, strings.Replace(publish, `"dev"`, `"prod"`, 1), alice, 403)
		request("POST", imports, publish, bob, 403)
		request("POST", imports, publish, alice, 409)
		var doc Document
		if err := json.Unmarshal(preview.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		doc.Metadata = Metadata{Team: "容器平台", ResourceType: "Pod", Applicability: "镜像拉取失败", Signals: []string{"ImagePullBackOff"},
			ReviewBy: time.Now().AddDate(0, 1, 0).Format(time.DateOnly),
			Sections: map[string]string{"conditions": "镜像拉取失败", "signals": "ImagePullBackOff", "checks": "检查仓库凭据", "recovery": "Pod Ready", "escalation": "通知容器平台"}}
		call := func(path string, payload any, want int) Candidate {
			t.Helper()
			data, _ := json.Marshal(payload)
			out := request("POST", path, string(data), alice, want)
			var result Candidate
			if want == 200 && json.Unmarshal(out.Body.Bytes(), &result) != nil {
				t.Fatal(out.Body.String())
			}
			return result
		}
		create := func(id string, revision int, candidate Document) string {
			return call("/api/v1/wiki/candidates", map[string]any{"document": candidate, "knowledgeId": id, "baseRevision": revision}, 200).ID
		}
		review := func(id string, candidate Document) Candidate {
			endpoint := "/api/v1/wiki/candidates/" + id
			call(endpoint, candidateAction{Action: "submit", Version: 1, Document: candidate}, 200)
			result := call(endpoint, candidateAction{Action: "publish", Version: 2}, 200)
			call(endpoint, candidateAction{Action: "publish", Version: 2}, 200)
			return result
		}
		published := review(create("", 0, doc), doc)
		doc.ID = published.KnowledgeID
		if out := request("GET", knowledge+"?documentType="+url.QueryEscape("排障手册"), "", alice, 200); !strings.Contains(out.Body.String(), `"total":1`) || strings.Contains(out.Body.String(), `"body"`) {
			t.Fatal(out.Body.String())
		}
		t.Run("orders all matched versions before paging", func(t *testing.T) {
			t.Cleanup(func() {
				if _, err := store.ExecContext(context.Background(), "DELETE FROM wiki_documents WHERE id LIKE 'sort-fixture-%'"); err != nil {
					t.Error(err)
				}
			})
			for i := 0; i < 21; i++ {
				title := fmt.Sprintf("候选文档 %02d", i)
				if i == 0 {
					title = "排序基准"
				}
				at := time.Date(2026, time.January, 1+i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
				if _, err := store.ExecContext(t.Context(), "INSERT INTO wiki_documents VALUES(?,?,?,?,?,?,?,?,?)",
					fmt.Sprintf("sort-fixture-%02d", i), "local", "dev", "测试", "运维", "排障手册", title, "排序基准", at); err != nil {
					t.Fatal(err)
				}
			}
			type searchPage struct {
				Items      []Document `json:"items"`
				NextCursor string     `json:"nextCursor"`
			}
			read := func(path string) searchPage {
				t.Helper()
				var page searchPage
				out := request("GET", path, "", alice, 200)
				if err := json.Unmarshal(out.Body.Bytes(), &page); err != nil || len(page.Items) == 0 {
					t.Fatalf("empty search page: %v, %s", err, out.Body)
				}
				return page
			}
			path := knowledge + "?q=" + url.QueryEscape("排序基准")
			if ranked := read(path); ranked.Items[0].ID != "sort-fixture-00" {
				t.Fatalf("default relevance changed: %s", ranked.Items[0].ID)
			}
			recent := read(path + "&sort=recent")
			if len(recent.Items) != 20 || recent.Items[0].ID != "sort-fixture-20" || recent.NextCursor == "" {
				t.Fatalf("first date-ordered page: %d items, first %s, cursor %q", len(recent.Items), recent.Items[0].ID, recent.NextCursor)
			}
			cursor := "&cursor=" + url.QueryEscape(recent.NextCursor)
			if last := read(path + "&sort=recent" + cursor); len(last.Items) != 1 || last.Items[0].ID != "sort-fixture-00" {
				t.Fatalf("date-ordered second page did not contain the oldest document: %+v", last.Items)
			}
			request("GET", path+cursor, "", alice, 400)
		})
		path := knowledge + "/" + doc.ID + "/revisions/1"
		request("GET", path, "", alice, 200)
		revision := knowledge + "/" + doc.ID + "/revisions"
		request("POST", revision, `{"expectedRevision":1,"title":"镜像拉取订正","body":"检查仓库凭据"}`, alice, 409)
		doc.Title = "镜像拉取订正"
		review(create(doc.ID, 1, doc), doc)
		request("POST", revision, `{"expectedRevision":1,"title":"旧版本","body":"应被拒绝"}`, alice, 409)
		if out := request("GET", revision+"/2", "", alice, 200); !strings.Contains(out.Body.String(), `"revision":2`) {
			t.Fatal(out.Body.String())
		}
		if out := request("GET", path, "", alice, 200); strings.Contains(out.Body.String(), "镜像拉取订正") {
			t.Fatal(out.Body.String())
		}

		const question = `{"question":"镜像拉取失败怎么办","environment":"testing"}`
		if out := request("POST", "/api/v1/wiki/ask", question, alice, 200); !strings.Contains(out.Body.String(), "请检查仓库凭据") || !strings.Contains(out.Body.String(), `"revision":2`) {
			t.Fatal(out.Body.String())
		}
		if out := request("POST", "/api/v1/wiki/ask", question, bob, 200); !strings.Contains(out.Body.String(), `"citations":[]`) {
			t.Fatal(out.Body.String())
		}

		feedback := knowledge + "/" + doc.ID + "/feedback"
		request("POST", feedback, `{"revision":1,"section":"does-not-exist","kind":"outdated"}`, alice, 400)
		request("POST", feedback, `{"revision":1,"kind":"outdated","note":"旧版需核对"}`, bob, 404)
		request("POST", feedback, `{"revision":1,"kind":"outdated","note":"旧版需核对"}`, alice, 204)
		request("POST", feedback, `{"revision":2,"kind":"outdated","note":"现行版需核对"}`, alice, 204)
		request("POST", feedback, `{"revision":2,"kind":"outdated","note":"现行版已补充线索"}`, alice, 204)
		out := request("GET", feedback, "", root, 200)
		var entries []feedbackEntry
		if json.Unmarshal(out.Body.Bytes(), &entries) != nil || len(entries) != 2 || !strings.Contains(out.Body.String(), "现行版已补充线索") {
			t.Fatal(out.Body.String())
		}
		if out := request("GET", "/api/v1/wiki/governance", "", root, 200); !strings.Contains(out.Body.String(), `"pendingFeedback":1`) {
			t.Fatal(out.Body.String())
		}
		request("POST", knowledge+"/"+doc.ID+"/governance", `{"action":"review","revision":2,"reviewBy":"2027-12-01"}`, root, 200)
		if out := request("GET", "/api/v1/wiki/governance", "", root, 200); strings.Contains(out.Body.String(), `"pendingFeedback"`) {
			t.Fatal(out.Body.String())
		}
		request("GET", path, "", bob, 404)
		if out := request("GET", knowledge, "", bob, 200); strings.Contains(out.Body.String(), "运维") || !strings.Contains(out.Body.String(), `"total":0`) {
			t.Fatal(out.Body.String())
		}
		revokeDuringAsk = func() {
			request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[]}`, root, 204)
		}
		defer func() { revokeDuringAsk = nil }()
		if out := request("POST", "/api/v1/wiki/ask", question, alice, 409); strings.Contains(out.Body.String(), "仓库凭据") {
			t.Fatal(out.Body.String())
		}
	})

	t.Run("manual and scheduled sync reuse Alice's binding", func(t *testing.T) {
		request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[{"effect":"allow","subjects":["local:alice"],"capabilities":["wiki:sync","wiki:read"],"scopes":[{"cluster":"local","namespace":"dev"}]}]}`, root, 204)
		if out := request("POST", source+"/sync", "", alice, 200); !strings.Contains(out.Body.String(), `"saved":1`) {
			t.Fatal(out.Body.String())
		}
		if out := request("POST", source+"/sync", "", alice, 200); !strings.Contains(out.Body.String(), `"skipped":1`) {
			t.Fatal(out.Body.String())
		}
		request("PUT", source+"/schedule", `{"intervalMinutes":1}`, alice, 204)
		if _, err := store.ExecContext(t.Context(), "UPDATE wiki_sync_schedules SET next_at=? WHERE source_id=?", time.Now().Add(-time.Second).Unix(), "ops"); err != nil {
			t.Fatal(err)
		}
		request("POST", "/api/v1/auth/logout", "", alice, 204)
		service.runDueWikiSchedule(t.Context())
		if remoteCalls.Load() != 3 {
			t.Fatalf("expected two manual and one scheduled remote login, got %d", remoteCalls.Load())
		}
		alice = login("alice", "fixture-alice-pass")
		if out := request("GET", source+"/schedule", "", alice, 200); !strings.Contains(out.Body.String(), `"intervalMinutes":1`) {
			t.Fatal(out.Body.String())
		}
		request("PUT", source+"/schedule", `{"intervalMinutes":0}`, alice, 204)
		addSecondPage.Store(true)
		syncFailure.Store(true)
		res := request("POST", source+"/sync", "", alice, 200)
		var failed wikiSyncResult
		if json.Unmarshal(res.Body.Bytes(), &failed) != nil || failed.RunID == "" || failed.Skipped != 1 || failed.Failed != 1 {
			t.Fatal(res.Body.String())
		}
		path := source + "/runs/" + failed.RunID
		if out := request("GET", path+"?failed=true", "", alice, 200); !strings.Contains(out.Body.String(), `"pageId":"page-2"`) ||
			strings.Contains(out.Body.String(), `"pageId":"page-1"`) {
			t.Fatal(out.Body.String())
		}
		reopened, err := identitystore.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		var status string
		err = reopened.QueryRow("SELECT status FROM wiki_sync_runs WHERE id=?", failed.RunID).Scan(&status)
		reopened.Close()
		if err != nil || status != "partial" {
			t.Fatalf("missing persisted failed run: %q %v", status, err)
		}
		request("GET", path, "", bob, 404)
		request("POST", path+"/retry", `{"pageId":"page-2"}`, bob, 404)
		request("POST", path+"/retry", `{"pageId":"page-1"}`, alice, 404)
		syncFailure.Store(false)
		if _, err := store.ExecContext(t.Context(), "UPDATE wiki_sync_runs SET finished_at=100 WHERE status='succeeded'"); err != nil {
			t.Fatal(err)
		}
		if out := request("POST", path+"/retry", `{"pageId":"page-2"}`, alice, 200); !strings.Contains(out.Body.String(), `"saved":1`) ||
			!strings.Contains(out.Body.String(), `"skipped":0`) {
			t.Fatal(out.Body.String())
		}
		if out := request("POST", path+"/retry", `{"pageId":"page-2"}`, alice, 200); !strings.Contains(out.Body.String(), `"skipped":1`) {
			t.Fatal(out.Body.String())
		}
		if out := request("GET", source+"/runs", "", alice, 200); !strings.Contains(out.Body.String(), `"lastSucceeded":100`) {
			t.Fatal(out.Body.String())
		}
		run, err := service.startWikiRun(t.Context(), "ops", "manual", "", "")
		if err != nil {
			t.Fatal(err)
		}
		request("POST", source+"/sync", "", alice, 409)
		if _, err := store.ExecContext(t.Context(), "UPDATE wiki_sync_runs SET lease_until=0 WHERE id=?", run); err != nil {
			t.Fatal(err)
		}
		if out := request("GET", source+"/runs/"+run, "", alice, 200); !strings.Contains(out.Body.String(), `"status":"interrupted"`) {
			t.Fatal(out.Body.String())
		}
		request("POST", source+"/runs/"+run+"/retry", `{}`, alice, 200)
		request("PUT", "/api/v1/admin/policy", `{"version":"v1","rules":[]}`, root, 204)
		request("POST", path+"/retry", `{"pageId":"page-2"}`, alice, 404)
	})
}

func TestWikiDOCXPlainText(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
		<w:p><w:r><w:t>排障手册</w:t></w:r></w:p><w:p><w:r><w:t>检查仓库权限</w:t></w:r></w:p>
		</w:body></w:document>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := wikiFileText("guide.docx", &buffer)
	if err != nil || out != "排障手册\n检查仓库权限\n" {
		t.Fatalf("unexpected DOCX extraction %q: %v", out, err)
	}

}

func TestWikiCronRespectsTimezone(t *testing.T) {
	schedule, err := parseWikiCron("0 2 * * *", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 11, 16, 17, 0, 0, 0, time.UTC)
	if got := schedule.next(after); !got.Equal(after.Add(time.Hour)) {
		t.Fatalf("unexpected next run %s", got)
	}
	if _, err := parseWikiCron("60 2 * * *", "Asia/Shanghai"); err == nil {
		t.Fatal("invalid Cron accepted")
	}
}

func writeIdentityTestFile(t *testing.T, variable, contents string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(variable, path)
}
