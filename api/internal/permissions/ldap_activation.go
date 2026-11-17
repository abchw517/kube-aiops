package permissions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const ldapVerificationLifetime = 5 * time.Minute

type ldapLoginProbe func(context.Context, identity.LDAPConfig, string, string) string

type ldapVerification struct {
	tokenHash [32]byte
	draftHash [32]byte
	subject   string
	expires   time.Time
}

func (s ldapSettings) normalized() ldapSettings {
	s.URL = strings.TrimSpace(s.URL)
	s.BindDN = strings.TrimSpace(s.BindDN)
	s.BaseDN = strings.TrimSpace(s.BaseDN)
	s.UserUID = strings.TrimSpace(s.UserUID)
	s.GroupAttr = strings.TrimSpace(s.GroupAttr)
	s.GroupBaseDN = strings.TrimSpace(s.GroupBaseDN)
	s.GroupFilter = strings.TrimSpace(s.GroupFilter)
	return s
}

func (a *Admin) handleLDAPChange(w http.ResponseWriter, r *http.Request, test bool) {
	ctx := r.Context()
	p, ok := identity.PrincipalFromContext(ctx)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var input struct {
		ldapSettings
		TestUsername string `json:"testUsername,omitempty"`
		TestPassword string `json:"testPassword,omitempty"`
		Verification string `json:"verification,omitempty"`
	}
	if !adminhttp.Decode(w, r, &input) {
		http.Error(w, "invalid LDAP configuration", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(input.TestUsername)
	if test && (username == "" || len(username) > 250 || strings.ContainsRune(username, 0) ||
		input.TestPassword == "" || len(input.TestPassword) > 1024 || strings.ContainsRune(input.TestPassword, 0)) {
		http.Error(w, "请输入有效的测试员工账号和密码", http.StatusBadRequest)
		return
	}
	if !test && (input.TestUsername != "" || input.TestPassword != "") {
		http.Error(w, "保存配置时不得提交测试员工凭据", http.StatusBadRequest)
		return
	}

	settings := input.ldapSettings.normalized()
	a.mu.RLock()
	current, revision := a.settings, a.ldapRevision
	a.mu.RUnlock()
	if settings.URL == "" {
		http.Error(w, "请填写 LDAP URL", http.StatusBadRequest)
		return
	}
	if settings.BindPassword == "" {
		if settings.URL != current.URL || settings.BindDN != current.BindDN {
			http.Error(w, "password_required", http.StatusBadRequest)
			return
		}
		settings.BindPassword = current.BindPassword
	}
	cfg, err := settings.configuration()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, _ := json.Marshal(settings)
	digest := sha256.Sum256(raw)

	if test {
		probe := a.ldapProbe
		if probe == nil {
			probe = identity.VerifyLDAPLogin
		}
		stage := probe(ctx, *cfg, username, input.TestPassword)
		var token string
		if stage == "success" {
			a.mu.Lock()
			if a.ldapRevision != revision {
				a.mu.Unlock()
				http.Error(w, "LDAP 配置已变化，请重新测试", http.StatusConflict)
				return
			}
			token = rand.Text()
			a.ldapVerification = ldapVerification{
				tokenHash: sha256.Sum256([]byte(token)), draftHash: digest,
				subject: p.Subject, expires: time.Now().Add(ldapVerificationLifetime),
			}
			a.mu.Unlock()
		}
		adminhttp.WriteJSON(w, struct {
			Stage        string `json:"stage"`
			Verification string `json:"verification,omitempty"`
		}{stage, token})
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	verification := a.ldapVerification
	if input.Verification == "" || verification.tokenHash != sha256.Sum256([]byte(input.Verification)) ||
		verification.subject != p.Subject || verification.draftHash != digest ||
		a.ldapRevision != revision || !time.Now().Before(verification.expires) {
		http.Error(w, "LDAP 配置未通过验证或验证已失效，请重新测试", http.StatusConflict)
		return
	}
	document, err := encodeLDAPSettings(settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if _, err := a.store.ExecContext(ctx, "INSERT INTO ldap_settings VALUES (1,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", string(document)); err != nil {
		http.Error(w, "identity storage unavailable", http.StatusServiceUnavailable)
		return
	}
	a.ldap, a.settings, a.ldapIssue = cfg, settings, ""
	a.ldapRevision++
	a.ldapVerification = ldapVerification{}
	slog.InfoContext(ctx, "identity administration audit", "operation", "ldap", "method", http.MethodPut, "subject", p.Subject)
	w.WriteHeader(http.StatusNoContent)
}
