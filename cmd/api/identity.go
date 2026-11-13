package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/abchw517/kube-aiops/internal/authorization"
	"github.com/abchw517/kube-aiops/internal/identity"
	"github.com/abchw517/kube-aiops/internal/identitystore"
	"github.com/abchw517/kube-aiops/internal/security"
	"golang.org/x/crypto/bcrypt"
)

func configureIdentity(bundle *security.Bundle) (*identity.Sessions, error) {
	providers := make(map[string]identity.PasswordVerifier)
	if path := os.Getenv("AUTH_LOCAL_ACCOUNTS_FILE"); path != "" {
		accounts, err := loadLocalAccounts(path)
		if err != nil {
			return nil, err
		}
		providers["local"] = func(_ context.Context, username, password string) (identity.Principal, error) {
			return authenticateLocal(accounts, username, password)
		}
	}
	if endpoint := os.Getenv("AUTH_LDAP_URL"); endpoint != "" {
		cfg, err := ldapConfiguration(endpoint)
		if err != nil {
			return nil, err
		}
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		providers["ldap"] = func(ctx context.Context, username, password string) (identity.Principal, error) {
			return identity.AuthenticateLDAPContext(ctx, cfg, username, password)
		}
	}
	if len(providers) == 0 {
		return nil, nil
	}
	policy, err := os.ReadFile(os.Getenv("AUTH_POLICY_FILE"))
	if err != nil {
		return nil, errors.New("cannot load authorization policy")
	}
	authorizer, err := authorization.ParsePolicyJSON(policy)
	if err != nil {
		return nil, errors.New("invalid authorization policy")
	}
	sessions, err := identity.NewSessions(os.Getenv("AUTH_ORIGIN"), providers)
	if err != nil {
		return nil, err
	}
	bundle.Authenticator, bundle.Authorizer = sessions, authorizer
	return sessions, nil
}

func authenticateLocal(accounts []identity.LocalAccount, username, password string) (identity.Principal, error) {
	p, err := identity.AuthenticateLocal(accounts, username, password)
	if err == nil {
		p.Subject = "local:" + p.Subject
		for i, group := range p.Groups {
			p.Groups[i] = "local:" + group
		}
	}
	return p, err
}

func loadLocalAccounts(path string) ([]identity.LocalAccount, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read local accounts file")
	}
	var accounts []identity.LocalAccount
	if json.Unmarshal(data, &accounts) != nil || accounts == nil {
		return nil, errors.New("local accounts file must contain a JSON account list")
	}
	seen := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		principal := identity.Principal{Subject: "local:" + account.Username, Provider: "local"}
		for _, group := range account.Groups {
			if group == "" || strings.TrimSpace(group) != group {
				return nil, errors.New("invalid local account group")
			}
			principal.Groups = append(principal.Groups, "local:"+group)
		}
		cost, err := bcrypt.Cost([]byte(account.Hash))
		if account.Username == "" || strings.TrimSpace(account.Username) != account.Username ||
			seen[account.Username] || err != nil || cost < bcrypt.DefaultCost || cost > 14 || principal.Validate() != nil {
			return nil, errors.New("invalid or duplicate local account")
		}
		seen[account.Username] = true
	}
	return accounts, nil
}

func importIdentity() error {
	path := os.Getenv("AUTH_SQLITE_PATH")
	if path == "" {
		return errors.New("AUTH_SQLITE_PATH is required")
	}
	policy, err := os.ReadFile(os.Getenv("AUTH_POLICY_FILE"))
	if err != nil {
		return errors.New("cannot read authorization policy file")
	}
	if _, err := authorization.ParsePolicyJSON(policy); err != nil {
		return errors.New("invalid authorization policy")
	}
	var accounts []identity.LocalAccount
	if source := os.Getenv("AUTH_LOCAL_ACCOUNTS_FILE"); source != "" {
		accounts, err = loadLocalAccounts(source)
		if err != nil {
			return err
		}
	}
	store, err := identitystore.Open(path)
	if err != nil {
		return errors.New("cannot open identity database")
	}
	defer store.Close()
	return store.Import(context.Background(), accounts, policy)
}

func ldapConfiguration(endpoint string) (identity.LDAPConfig, error) {
	cfg := identity.LDAPConfig{
		URL: endpoint, BaseDN: os.Getenv("AUTH_LDAP_BASE_DN"),
		BindDN: os.Getenv("AUTH_LDAP_BIND_DN"), UserFilter: os.Getenv("AUTH_LDAP_USER_FILTER"),
		GroupAttr: os.Getenv("AUTH_LDAP_GROUP_ATTRIBUTE"),
	}
	password, err := os.ReadFile(os.Getenv("AUTH_LDAP_BIND_PASSWORD_FILE"))
	if err != nil {
		return cfg, errors.New("cannot read LDAP bind password file")
	}
	cfg.BindPassword = strings.TrimSuffix(string(password), "\n")
	if path := os.Getenv("AUTH_LDAP_CA_FILE"); path != "" {
		pem, err := os.ReadFile(path)
		pool := x509.NewCertPool()
		if err != nil || !pool.AppendCertsFromPEM(pem) {
			return cfg, errors.New("cannot load LDAP CA certificate")
		}
		cfg.TLSConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return cfg, nil
}
