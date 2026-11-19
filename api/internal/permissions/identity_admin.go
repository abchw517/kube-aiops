package permissions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"golang.org/x/crypto/bcrypt"
)

const (
	initialAdminPassword = "admin123"
	AdminProvider        = "managed-admin"
	InitialAdminProvider = "initial-admin"
)

type ldapSettings struct {
	URL            string `json:"url"`
	AllowPlainLDAP bool   `json:"allowPlainLDAP,omitempty"`
	BaseDN         string `json:"baseDN"`
	BindDN         string `json:"bindDN"`
	UserFilter     string `json:"userFilter"`
	GroupAttr      string `json:"groupAttribute"`
	BindPassword   string `json:"bindPassword,omitempty"`
	UserUID        string `json:"userUID,omitempty"`
	SearchScope    string `json:"searchScope,omitempty"`
	GroupBaseDN    string `json:"groupBaseDN,omitempty"`
	GroupFilter    string `json:"groupFilter,omitempty"`
	GroupScope     string `json:"groupScope,omitempty"`
}

type Admin struct {
	store              *identitystore.Store
	origin             string
	mu                 sync.RWMutex
	policy             *authorization.PolicyAuthorizer
	basePolicy         authorization.Policy
	roles              []authorization.Role
	ldap               *identity.LDAPConfig
	settings           ldapSettings
	ldapIssue          string
	ldapRevision       uint64
	ldapVerification   ldapVerification
	ldapProbe          ldapLoginProbe
	clusterEnvironment ClusterScopeResolver
	namespaceBusiness  NamespaceBusinessResolver
}

func New(ctx context.Context, store *identitystore.Store) (*Admin, error) {
	a := &Admin{store: store, origin: os.Getenv("AUTH_ORIGIN")}
	if os.Getenv("AUTH_BOOTSTRAP_ADMIN") == "true" {
		if err := bootstrapAdministrator(ctx, store); err != nil {
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
	a.basePolicy, err = authorization.DecodePolicyJSON(policy)
	if err != nil {
		return nil, err
	}
	roles, err := store.Roles(ctx)
	if err == nil {
		if err := json.Unmarshal(roles, &a.roles); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	a.policy, err = a.basePolicy.WithRoles(a.roles)
	if err != nil {
		return nil, err
	}
	var document []byte
	err = store.QueryRowContext(ctx, "SELECT document FROM ldap_settings WHERE id=1").Scan(&document)
	switch {
	case err == nil:
		a.settings, err = loadLDAPSettings(ctx, store, document)
	case errors.Is(err, sql.ErrNoRows):
		a.settings = ldapSettings{
			URL: os.Getenv("AUTH_LDAP_URL"), BaseDN: os.Getenv("AUTH_LDAP_BASE_DN"),
			BindDN: os.Getenv("AUTH_LDAP_BIND_DN"), UserFilter: os.Getenv("AUTH_LDAP_USER_FILTER"),
			GroupAttr: os.Getenv("AUTH_LDAP_GROUP_ATTRIBUTE"), AllowPlainLDAP: os.Getenv("AUTH_LDAP_ALLOW_PLAIN") == "true",
		}
		err = nil
	default:
		return nil, err
	}
	if err == nil {
		a.ldap, err = a.settings.configuration()
	}
	if err != nil {
		a.ldapIssue = err.Error()
		slog.WarnContext(ctx, "LDAP disabled due to configuration error", "reason", a.ldapIssue)
	}
	return a, nil
}

func bootstrapAdministrator(ctx context.Context, store *identitystore.Store) error {
	if _, _, err := store.Admin(ctx); !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := store.LocalAccount(ctx, "admin"); err == nil {
		return errors.New("local admin already exists; migrate it before enabling administrator bootstrap")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(initialAdminPassword), 12)
	if err != nil {
		return err
	}
	return store.BootstrapAdmin(ctx, string(hash))
}

func (s ldapSettings) configuration() (*identity.LDAPConfig, error) {
	if s.URL == "" {
		return nil, nil
	}
	cfg := identity.LDAPConfig{URL: s.URL, BaseDN: s.BaseDN, BindDN: s.BindDN, UserFilter: s.UserFilter, GroupAttr: s.GroupAttr, UserUID: s.UserUID, SearchScope: s.SearchScope, GroupBaseDN: s.GroupBaseDN, GroupFilter: s.GroupFilter, GroupScope: s.GroupScope, AllowPlainLDAP: s.AllowPlainLDAP}
	if err := applyLDAPCredentials(&cfg, s.BindPassword); err != nil {
		return nil, err
	}

	return &cfg, cfg.Validate()
}

func (a *Admin) Authorize(ctx context.Context, r authorization.DecisionRequest) (authorization.Decision, error) {
	if IsAdministrator(r.Principal) {
		return authorization.Decision{Allowed: true}, nil
	}
	switch r.Principal.Provider {
	case InitialAdminProvider, pendingProvider:
		return authorization.Decision{}, nil
	case guestProvider, employeeProvider:
		return a.authorizePlatform(ctx, r)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.policy.Authorize(ctx, r)
}

func (a *Admin) Local(ctx context.Context, user, pass string) (identity.Principal, error) {
	if user == "admin" {
		hash, initial, err := a.store.Admin(ctx)
		if err == nil {
			if len(pass) > 72 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) != nil {
				return identity.Principal{}, identity.ErrUnauthenticated
			}
			provider := AdminProvider
			if initial {
				provider = InitialAdminProvider
			}
			return identity.Principal{Subject: "local:admin", Provider: provider, SessionVersion: versionOf(hash)}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return identity.Principal{}, identity.ErrAuthenticationUnavailable
		}
	}
	if principal, found, err := a.platformLocal(ctx, user, pass); found {
		return principal, err
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

func (a *Admin) Directory(ctx context.Context, user, pass string) (identity.Principal, error) {
	a.mu.RLock()
	cfg, issue := a.ldap, a.ldapIssue
	a.mu.RUnlock()
	if issue != "" {
		return identity.Principal{}, identity.ErrAuthenticationUnavailable
	}
	if cfg == nil {
		return identity.Principal{}, identity.ErrUnauthenticated
	}
	principal, err := identity.AuthenticateLDAPContext(ctx, *cfg, user, pass)
	if err != nil {
		return principal, err
	}
	return a.registerEmployee(ctx, user, principal)
}

func validNewPassword(password string) bool {
	return len(password) >= 12 && len(password) <= 72 && !strings.ContainsRune(password, 0)
}

func (a *Admin) Handle(w http.ResponseWriter, r *http.Request, resource string, sessions *identity.Sessions) {
	ctx := r.Context()
	var err error
	actor, _ := identity.PrincipalFromContext(ctx)
	switch {
	case resource == "accounts" || resource == "access" || resource == "operations":
		a.handlePlatform(w, r, resource, actor)
		return
	case resource == "password" && r.Method == http.MethodPost:
		var input struct{ Current, Password string }
		if !adminhttp.Decode(w, r, &input) || !validNewPassword(input.Password) || len(input.Current) > 72 {
			http.Error(w, "invalid password", 400)
			return
		}
		if actor.Subject != "" && actor.Subject != superSubject {
			err = a.changePlatformPassword(ctx, actor, input.Current, input.Password)
			if errors.Is(err, identity.ErrUnauthenticated) {
				http.Error(w, "当前密码错误或会话已失效", 400)
				return
			}
			if err != nil {
				break
			}
			sessions.RevokeSubject(actor.Subject)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		hash, _, readErr := a.store.Admin(ctx)
		if readErr != nil {
			err = readErr
			break
		}
		if (actor.SessionVersion != "" && versionOf(hash) != actor.SessionVersion) || bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Current)) != nil {
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
		sessions.RevokeSubject(superSubject)
		a.RecordOperation(ctx, superSubject, "修改密码", "高", superSubject)
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
			adminhttp.WriteJSON(w, users)
			return
		}
	case resource == "users" && r.Method == http.MethodPost:
		var input struct {
			Username, Password string
			Groups             []string
		}
		if !adminhttp.Decode(w, r, &input) || !validNewPassword(input.Password) || input.Username == "admin" || input.Username == "" || strings.TrimSpace(input.Username) != input.Username {
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
			adminhttp.WriteJSON(w, json.RawMessage(document))
			return
		}
	case resource == "policy" && r.Method == http.MethodPut:
		var document json.RawMessage
		if !adminhttp.Decode(w, r, &document) {
			http.Error(w, "invalid policy", 400)
			return
		}
		base, parseErr := authorization.DecodePolicyJSON(document)
		if parseErr != nil {
			http.Error(w, "invalid policy", 400)
			return
		}
		a.mu.Lock()
		policy, parseErr := base.WithRoles(a.roles)
		if parseErr != nil {
			a.mu.Unlock()
			http.Error(w, "invalid policy", 400)
			return
		}
		err = a.store.Import(ctx, nil, document)
		if err == nil {
			a.basePolicy, a.policy = base, policy
		}
		a.mu.Unlock()
	case resource == "roles":
		a.handleRoles(w, r)
		return
	case resource == "ldap" && r.Method == http.MethodGet:
		a.mu.RLock()
		settings := a.settings
		configured := settings.BindPassword != "" || a.ldap != nil && a.ldap.BindPassword != ""
		issue := a.ldapIssue
		a.mu.RUnlock()
		settings.BindPassword = ""
		adminhttp.WriteJSON(w, struct {
			ldapSettings
			PasswordConfigured bool   `json:"passwordConfigured"`
			Issue              string `json:"issue,omitempty"`
		}{settings, configured, issue})
		return
	case (resource == "ldap" && r.Method == http.MethodPut) ||
		(resource == "ldap-test" && r.Method == http.MethodPost):
		a.handleLDAPChange(w, r, resource == "ldap-test")
		return
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

func (a *Admin) Origin() string { return a.origin }
