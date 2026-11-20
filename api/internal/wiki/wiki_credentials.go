package wiki

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

var wikiSourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type wikiSource struct {
	URL       string `json:"url"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
}

type wikiCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *Service) wikiSourceHandler(s *identity.Sessions, handle func(http.ResponseWriter, *http.Request, identity.Principal, string)) http.HandlerFunc {
	return a.wikiSessionHandler(s, func(w http.ResponseWriter, r *http.Request, principal identity.Principal) {
		id := r.PathValue("id")
		if !wikiSourceID.MatchString(id) {
			http.Error(w, "无效的 Wiki 来源编号", http.StatusBadRequest)
			return
		}
		handle(w, r, principal, id)
	})
}

func (a *Service) wikiSessionHandler(s *identity.Sessions, handle func(http.ResponseWriter, *http.Request, identity.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		principal, err := s.Authenticate(r.Context(), r)
		if err != nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		origins := r.Header.Values("Origin")
		if principal.Provider == permissions.InitialAdminProvider || (r.Method != http.MethodGet && (len(origins) != 1 || origins[0] != a.origin)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ctx := WithSessionRecheck(withTargetCache(r.Context()), func(ctx context.Context) bool {
			current, err := s.Authenticate(ctx, r)
			return err == nil && current.Subject == principal.Subject && current.Provider == principal.Provider
		})
		handle(w, r.WithContext(ctx), principal)
	}
}

// Sources are immutable in this implementation. A different remote URL gets a
// new ID, so previously stored credentials cannot follow a changed endpoint.
func (a *Service) handleWikiSource(w http.ResponseWriter, r *http.Request, principal identity.Principal, id string) {
	if !permissions.IsAdministrator(principal) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodDelete {
		ctx := r.Context()
		tx, err := a.store.BeginTx(ctx, nil)
		if err != nil {
			http.Error(w, "无法停用来源", 503)
			return
		}
		defer tx.Rollback()
		doc := Document{ID: id}
		if err := tx.QueryRowContext(ctx, "SELECT cluster,namespace FROM wiki_sources WHERE id=?", id).Scan(&doc.Cluster, &doc.Namespace); err != nil {
			http.NotFound(w, r)
			return
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO wiki_disabled_sources(id) VALUES(?) ON CONFLICT(id) DO NOTHING", id)
		if err != nil {
			http.Error(w, "无法停用来源", 503)
			return
		}
		changed, err := result.RowsAffected()
		if err != nil || changed == 1 && writeWikiAudit(ctx, tx, principal, "停用知识来源", doc, true) != nil || tx.Commit() != nil {
			http.Error(w, "无法停用来源", 503)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var source wikiSource
	if !adminhttp.Decode(w, r, &source) {
		http.Error(w, "无效的 Wiki 来源", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(source.URL)
	scope := authorization.Scope{Cluster: source.Cluster, Namespace: source.Namespace}
	if err != nil || len(source.URL) > 2048 || target.Scheme != "https" || target.Hostname() == "" ||
		target.User != nil || target.Fragment != "" || target.RawQuery != "" || scope.Validate() != nil ||
		source.Cluster == "*" || source.Namespace == "" || source.Namespace == "*" {
		http.Error(w, "需要 HTTPS 目录地址、集群和命名空间", http.StatusBadRequest)
		return
	}
	_, err = a.store.ExecContext(r.Context(), "INSERT INTO wiki_sources VALUES (?, ?, ?, ?) ON CONFLICT(id) DO NOTHING", id, source.URL, source.Cluster, source.Namespace)
	if err == nil {
		var saved wikiSource
		err = a.store.QueryRowContext(r.Context(), "SELECT url, cluster, namespace FROM wiki_sources WHERE id=?", id).Scan(&saved.URL, &saved.Cluster, &saved.Namespace)
		if err == nil && saved != source {
			http.Error(w, "该来源编号已使用", http.StatusConflict)
			return
		}
	}
	if err != nil {
		http.Error(w, "无法保存 Wiki 来源", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Service) handleWikiCredential(w http.ResponseWriter, r *http.Request, principal identity.Principal, id string) {
	ctx := r.Context()
	var source wikiSource
	err := a.store.QueryRowContext(ctx, "SELECT url, cluster, namespace FROM wiki_sources WHERE id=?", id).Scan(&source.URL, &source.Cluster, &source.Namespace)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "无法读取 Wiki 来源", http.StatusServiceUnavailable)
		return
	}
	if err == nil {
		decision, authErr := a.Authorize(ctx, authorization.DecisionRequest{
			Principal: principal, Capability: authorization.CapabilityWikiSync,
			Scope: authorization.NamespaceScope(source.Cluster, source.Namespace),
		})
		if authErr != nil {
			http.Error(w, "无法校验 Wiki 权限", http.StatusServiceUnavailable)
			return
		}
		if !decision.Allowed {
			err = sql.ErrNoRows
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		var username string
		err = a.store.QueryRowContext(ctx, "SELECT username FROM wiki_source_credentials WHERE source_id=? AND provider=? AND subject=?", id, principal.Provider, principal.Subject).Scan(&username)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "无法读取 Wiki 凭据状态", http.StatusServiceUnavailable)
			return
		}
		adminhttp.WriteJSON(w, struct {
			Username   string `json:"username,omitempty"`
			Configured bool   `json:"configured"`
		}{username, err == nil})
	case http.MethodPut:
		var input wikiCredential
		if !adminhttp.Decode(w, r, &input) || input.Username == "" || strings.TrimSpace(input.Username) != input.Username ||
			len(input.Username) > 256 || len(input.Password) == 0 || len(input.Password) > 4096 ||
			strings.ContainsAny(input.Username, "\r\n\x00") || strings.ContainsRune(input.Password, 0) {
			http.Error(w, "需要有效的 Wiki 账号和密码", http.StatusBadRequest)
			return
		}
		aead, err := wikiPasswordCipher()
		if err == nil {
			ciphertext := aead.Seal(nil, nil, []byte(input.Password), wikiPasswordContext(id, source.URL, principal, input.Username))
			_, err = a.store.ExecContext(ctx, `INSERT INTO wiki_source_credentials VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(source_id, provider, subject) DO UPDATE SET username=excluded.username, password_ciphertext=excluded.password_ciphertext`,
				id, principal.Provider, principal.Subject, input.Username, ciphertext)
		}
		if err != nil {
			http.Error(w, "无法加密或保存 Wiki 凭据", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		_, err = a.store.ExecContext(ctx, "DELETE FROM wiki_source_credentials WHERE source_id=? AND provider=? AND subject=?", id, principal.Provider, principal.Subject)
		if err != nil {
			http.Error(w, "无法删除 Wiki 凭据", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func wikiPasswordCipher() (cipher.AEAD, error) {
	file, err := os.ReadFile(os.Getenv("WIKI_CREDENTIAL_ENCRYPTION_KEY_FILE"))
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(file)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("Wiki encryption key must contain 32 Base64-encoded bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func wikiPasswordContext(id, directoryURL string, principal identity.Principal, username string) []byte {
	data, _ := json.Marshal([6]string{"kube-aiops/wiki-password/v1", id, directoryURL, principal.Provider, principal.Subject, username})
	return data
}

// loadWikiCredential is a server-side lookup for the remote connector.
func (a *Service) loadWikiCredential(ctx context.Context, id string, principal identity.Principal) (wikiCredential, error) {
	var credential wikiCredential
	var directoryURL string
	var ciphertext []byte
	err := a.store.QueryRowContext(ctx, `SELECT c.username, c.password_ciphertext, s.url FROM wiki_source_credentials c
		JOIN wiki_sources s ON s.id=c.source_id WHERE c.source_id=? AND c.provider=? AND c.subject=?`,
		id, principal.Provider, principal.Subject).Scan(&credential.Username, &ciphertext, &directoryURL)
	if err != nil {
		return wikiCredential{}, err
	}
	aead, err := wikiPasswordCipher()
	if err != nil {
		return wikiCredential{}, err
	}
	password, err := aead.Open(nil, nil, ciphertext, wikiPasswordContext(id, directoryURL, principal, credential.Username))
	if err != nil {
		return wikiCredential{}, err
	}
	credential.Password = string(password)
	return credential, nil
}
