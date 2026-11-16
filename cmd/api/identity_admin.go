package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/identitystore"
	"golang.org/x/crypto/bcrypt"
)

const (
	initialAdminPassword = "admin123"
	adminProvider        = "managed-admin"
	initialAdminProvider = "initial-admin"
)

type ldapSettings struct {
	URL          string `json:"url"`
	BaseDN       string `json:"baseDN"`
	BindDN       string `json:"bindDN"`
	UserFilter   string `json:"userFilter"`
	GroupAttr    string `json:"groupAttribute"`
	BindPassword string `json:"bindPassword,omitempty"`
	UserUID      string `json:"userUID,omitempty"`
	SearchScope  string `json:"searchScope,omitempty"`
}

type identityAdmin struct {
	store    *identitystore.Store
	origin   string
	mu       sync.RWMutex
	policy   *authorization.PolicyAuthorizer
	ldap     *identity.LDAPConfig
	settings ldapSettings
}

func newIdentityAdmin(ctx context.Context, store *identitystore.Store) (*identityAdmin, error) {
	a := &identityAdmin{store: store, origin: os.Getenv("AUTH_ORIGIN")}
	if os.Getenv("AUTH_BOOTSTRAP_ADMIN") == "true" {
		if _, _, err := store.Admin(ctx); errors.Is(err, sql.ErrNoRows) {
			if _, err := store.LocalAccount(ctx, "admin"); err == nil {
				return nil, errors.New("local admin already exists; migrate it before enabling administrator bootstrap")
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(initialAdminPassword), 12)
			if err != nil {
				return nil, err
			}
			if err := store.BootstrapAdmin(ctx, string(hash)); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
	}
	policy, err := store.Policy(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		if _, _, adminErr := store.Admin(ctx); adminErr != nil {
			return nil, err
		}
		policy = []byte(`{"version":"v1","rules":[]}`)
		if err = store.Import(ctx, nil, policy); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	a.policy, err = authorization.ParsePolicyJSON(policy)
	if err != nil {
		return nil, err
	}
	var document []byte
	err = store.QueryRowContext(ctx, "SELECT document FROM ldap_settings WHERE id=1").Scan(&document)
	if err == nil {
		if err = json.Unmarshal(document, &a.settings); err != nil {
			return nil, err
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		a.settings = ldapSettings{os.Getenv("AUTH_LDAP_URL"), os.Getenv("AUTH_LDAP_BASE_DN"), os.Getenv("AUTH_LDAP_BIND_DN"), os.Getenv("AUTH_LDAP_USER_FILTER"), os.Getenv("AUTH_LDAP_GROUP_ATTRIBUTE"), "", "", ""}
	} else {
		return nil, err
	}
	a.ldap, err = a.settings.configuration()
	return a, err
}

func (s ldapSettings) configuration() (*identity.LDAPConfig, error) {
	if s.URL == "" {
		return nil, nil
	}
	cfg := identity.LDAPConfig{URL: s.URL, BaseDN: s.BaseDN, BindDN: s.BindDN, UserFilter: s.UserFilter, GroupAttr: s.GroupAttr, UserUID: s.UserUID, SearchScope: s.SearchScope}
	if err := applyLDAPCredentials(&cfg, s.BindPassword); err != nil {
		return nil, err
	}

	return &cfg, cfg.Validate()
}

func (a *identityAdmin) Authorize(ctx context.Context, r authorization.DecisionRequest) (authorization.Decision, error) {
	switch r.Principal.Provider {
	case initialAdminProvider:
		return authorization.Decision{}, nil
	case adminProvider:
		return authorization.Decision{Allowed: true}, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.policy.Authorize(ctx, r)
}

func (a *identityAdmin) local(ctx context.Context, user, pass string) (identity.Principal, error) {
	if user == "admin" {
		hash, initial, err := a.store.Admin(ctx)
		if err == nil {
			if len(pass) > 72 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) != nil {
				return identity.Principal{}, identity.ErrUnauthenticated
			}
			provider := adminProvider
			if initial {
				provider = initialAdminProvider
			}
			return identity.Principal{Subject: "local:admin", Provider: provider}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return identity.Principal{}, identity.ErrAuthenticationUnavailable
		}
	}
	account, err := a.store.LocalAccount(ctx, user)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Principal{}, identity.ErrUnauthenticated
	}
	if err != nil {
		return identity.Principal{}, identity.ErrAuthenticationUnavailable
	}
	return authenticateLocal([]identity.LocalAccount{account}, user, pass)
}

func (a *identityAdmin) directory(ctx context.Context, user, pass string) (identity.Principal, error) {
	a.mu.RLock()
	cfg := a.ldap
	a.mu.RUnlock()
	if cfg == nil {
		return identity.Principal{}, identity.ErrUnauthenticated
	}
	principal, err := identity.AuthenticateLDAPContext(ctx, *cfg, user, pass)
	if err != nil {
		return principal, err
	}
	if err := a.store.RecordLDAPLogin(ctx, user, principal); err != nil {
		return identity.Principal{}, identity.ErrAuthenticationUnavailable
	}
	return principal, nil
}

func (a *identityAdmin) wrap(s *identity.Sessions, next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/admin/{resource}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, err := s.Authenticate(r.Context(), r)
		if err != nil {
			http.Error(w, "authentication required", 401)
			return
		}
		resource := r.PathValue("resource")
		if resource == "status" && r.Method == http.MethodGet {
			writeAdminJSON(w, map[string]bool{"admin": p.Provider == adminProvider, "mustChangePassword": p.Provider == initialAdminProvider})
			return
		}
		if p.Provider != adminProvider && !(p.Provider == initialAdminProvider && resource == "password") {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method != http.MethodGet {
			origins := r.Header.Values("Origin")
			if len(origins) != 1 || origins[0] != a.origin {
				http.Error(w, "forbidden", 403)
				return
			}
		}
		a.handle(w, r, resource, s)
	})
	mux.Handle("/", next)
	return mux
}

func writeAdminJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func decodeAdmin(w http.ResponseWriter, r *http.Request, target any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(target) == nil && d.Decode(new(any)) == io.EOF
}

func validNewPassword(password string) bool {
	return len(password) >= 12 && len(password) <= 72 && !strings.ContainsRune(password, 0)
}

func (a *identityAdmin) handle(w http.ResponseWriter, r *http.Request, resource string, sessions *identity.Sessions) {
	ctx := r.Context()
	var err error
	switch {
	case resource == "password" && r.Method == http.MethodPost:
		var input struct{ Current, Password string }
		if !decodeAdmin(w, r, &input) || !validNewPassword(input.Password) || len(input.Current) > 72 {
			http.Error(w, "invalid password", 400)
			return
		}
		hash, _, readErr := a.store.Admin(ctx)
		if readErr != nil {
			err = readErr
			break
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Current)) != nil {
			http.Error(w, "invalid current password", 400)
			return
		}
		next, hashErr := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
		if hashErr != nil {
			err = hashErr
			break
		}
		changed, changeErr := a.store.ChangeAdmin(ctx, hash, string(next))
		if changeErr != nil {
			err = changeErr
			break
		}
		if !changed {
			http.Error(w, "password already changed", 409)
			return
		}
		sessions.RevokeSubject("local:admin")
	case resource == "users" && r.Method == http.MethodGet:
		rows, queryErr := a.store.QueryContext(ctx, "SELECT username, groups_json FROM local_accounts ORDER BY username")
		if queryErr != nil {
			err = queryErr
			break
		}
		defer rows.Close()
		users := make([]map[string]any, 0)
		for rows.Next() {
			var username string
			var groups json.RawMessage
			if err = rows.Scan(&username, &groups); err != nil {
				break
			}
			users = append(users, map[string]any{"username": username, "groups": groups})
		}
		if err == nil {
			err = rows.Err()
		}
		if err == nil {
			writeAdminJSON(w, users)
			return
		}
	case resource == "users" && r.Method == http.MethodPost:
		var input struct {
			Username, Password string
			Groups             []string
		}
		if !decodeAdmin(w, r, &input) || !validNewPassword(input.Password) || input.Username == "admin" || input.Username == "" || strings.TrimSpace(input.Username) != input.Username {
			http.Error(w, "invalid account", 400)
			return
		}
		principal := identity.Principal{Subject: "local:" + input.Username, Provider: "local"}
		for _, group := range input.Groups {
			if group == "" || strings.TrimSpace(group) != group {
				http.Error(w, "invalid group", 400)
				return
			}
			principal.Groups = append(principal.Groups, "local:"+group)
		}
		if principal.Validate() != nil {
			http.Error(w, "invalid account", 400)
			return
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
		if hashErr != nil {
			err = hashErr
			break
		}
		groups, _ := json.Marshal(input.Groups)
		result, insertErr := a.store.ExecContext(ctx, "INSERT INTO local_accounts VALUES (?,?,?) ON CONFLICT(username) DO NOTHING", input.Username, string(hash), string(groups))
		if insertErr != nil {
			err = insertErr
			break
		}
		count, countErr := result.RowsAffected()
		if countErr != nil {
			err = countErr
			break
		}
		if count == 0 {
			http.Error(w, "account already exists", 409)
			return
		}
	case resource == "policy" && r.Method == http.MethodGet:
		var document []byte
		document, err = a.store.Policy(ctx)
		if err == nil {
			writeAdminJSON(w, json.RawMessage(document))
			return
		}
	case resource == "policy" && r.Method == http.MethodPut:
		var document json.RawMessage
		if !decodeAdmin(w, r, &document) {
			http.Error(w, "invalid policy", 400)
			return
		}
		policy, parseErr := authorization.ParsePolicyJSON(document)
		if parseErr != nil {
			http.Error(w, "invalid policy", 400)
			return
		}
		a.mu.Lock()
		err = a.store.Import(ctx, nil, document)
		if err == nil {
			a.policy = policy
		}
		a.mu.Unlock()
	case resource == "ldap" && r.Method == http.MethodGet:
		a.mu.RLock()
		settings := a.settings
		configured := a.ldap != nil && a.ldap.BindPassword != ""
		a.mu.RUnlock()
		settings.BindPassword = ""
		writeAdminJSON(w, struct {
			ldapSettings
			PasswordConfigured bool `json:"passwordConfigured"`
		}{settings, configured})
		return
	case (resource == "ldap" && r.Method == http.MethodPut) ||
		(resource == "ldap-test" && r.Method == http.MethodPost):
		var settings ldapSettings
		if !decodeAdmin(w, r, &settings) {
			http.Error(w, "invalid LDAP configuration", 400)
			return
		}
		a.mu.Lock()
		if settings.URL != "" && settings.BindPassword == "" {
			if settings.URL != a.settings.URL || settings.BindDN != a.settings.BindDN {
				a.mu.Unlock()
				http.Error(w, "password_required", 400)
				return
			}
			settings.BindPassword = a.settings.BindPassword
		}
		cfg, configErr := settings.configuration()
		if configErr != nil {
			a.mu.Unlock()
			http.Error(w, configErr.Error(), 400)
			return
		}
		if resource == "ldap-test" && cfg == nil {
			a.mu.Unlock()
			http.Error(w, "请填写 LDAP URL 后再测试", 400)
			return
		}
		if resource == "ldap-test" {
			a.mu.Unlock()
			if stage := identity.TestLDAP(ctx, *cfg); stage != "" {
				writeAdminJSON(w, map[string]string{"stage": stage})
				return
			}
			writeAdminJSON(w, map[string]string{"stage": "success"})
			return
		}
		if cfg == nil {
			settings.BindPassword = ""
		}
		document, _ := json.Marshal(settings)
		_, err = a.store.ExecContext(ctx, "INSERT INTO ldap_settings VALUES (1,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", string(document))
		if err == nil {
			a.ldap, a.settings = cfg, settings
		}
		a.mu.Unlock()
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "identity storage unavailable", 503)
		return
	}
	slog.InfoContext(ctx, "identity administration audit", "operation", resource, "method", r.Method)
	w.WriteHeader(http.StatusNoContent)
}
