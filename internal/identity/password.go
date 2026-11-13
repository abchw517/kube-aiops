package identity

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"golang.org/x/crypto/bcrypt"
)

// LocalAccount stores a bcrypt hash and the groups assigned by the operator.
type LocalAccount struct {
	Username string   `json:"username"`
	Hash     string   `json:"passwordHash"`
	Groups   []string `json:"groups,omitempty"`
}

// AuthenticateLocal validates an account without accepting an empty password.
func AuthenticateLocal(accounts []LocalAccount, username, password string) (Principal, error) {
	if username == "" || password == "" || len(password) > 72 {
		return Principal{}, ErrUnauthenticated
	}
	for _, account := range accounts {
		if account.Username != username {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(account.Hash), []byte(password)) != nil {
			return Principal{}, ErrUnauthenticated
		}
		p := Principal{Subject: account.Username, Provider: "local", Groups: slices.Clone(account.Groups)}
		if err := p.Validate(); err != nil {
			return Principal{}, ErrAuthenticationUnavailable
		}
		return p, nil
	}
	return Principal{}, ErrUnauthenticated
}

// LDAPConfig describes a TLS-protected LDAP directory, including Active Directory.
type LDAPConfig struct {
	URL          string
	BaseDN       string
	BindDN       string
	BindPassword string
	UserFilter   string // Exactly one %s placeholder, escaped as an LDAP filter value.
	GroupAttr    string
	TLSConfig    *tls.Config
}

func (cfg LDAPConfig) Validate() error {
	endpoint, err := url.Parse(cfg.URL)
	if err != nil || endpoint.Scheme != "ldaps" || endpoint.Hostname() == "" ||
		endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		cfg.BaseDN == "" || cfg.BindDN == "" || cfg.BindPassword == "" ||
		strings.Count(cfg.UserFilter, "%s") != 1 || strings.Count(cfg.UserFilter, "%") != 1 ||
		(cfg.TLSConfig != nil && cfg.TLSConfig.InsecureSkipVerify) {
		return errors.New("invalid LDAPS identity configuration")
	}
	if _, err := ldap.CompileFilter(fmt.Sprintf(cfg.UserFilter, "validation")); err != nil {
		return errors.New("invalid LDAP user filter")
	}
	return nil
}

func AuthenticateLDAP(cfg LDAPConfig, username, password string) (Principal, error) {
	return AuthenticateLDAPContext(context.Background(), cfg, username, password)
}

// AuthenticateLDAPContext searches as the service account and verifies the user's bind.
func AuthenticateLDAPContext(ctx context.Context, cfg LDAPConfig, username, password string) (Principal, error) {
	if username == "" || password == "" {
		return Principal{}, ErrUnauthenticated
	}
	if cfg.Validate() != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	endpoint, _ := url.Parse(cfg.URL)
	port := endpoint.Port()
	if port == "" {
		port = "636"
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSConfig != nil {
		tlsConfig = cfg.TLSConfig.Clone()
		tlsConfig.MinVersion = max(tlsConfig.MinVersion, tls.VersionTLS12)
	}
	tlsConfig.ServerName = endpoint.Hostname()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	dialer := tls.Dialer{Config: tlsConfig}
	socket, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(endpoint.Hostname(), port))
	if err != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	stop := context.AfterFunc(ctx, func() { _ = socket.Close() })
	defer stop()
	conn := ldap.NewConn(socket, true)
	conn.Start()
	defer conn.Close()
	conn.SetTimeout(5 * time.Second)
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	attributes := []string{"dn"}
	if cfg.GroupAttr != "" {
		attributes = append(attributes, cfg.GroupAttr)
	}
	search := ldap.NewSearchRequest(cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, 5, false, fmt.Sprintf(cfg.UserFilter, ldap.EscapeFilter(username)), attributes, nil)
	result, err := conn.Search(search)
	if err != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	if len(result.Entries) != 1 {
		return Principal{}, ErrUnauthenticated
	}
	entry := result.Entries[0]
	if strings.TrimSpace(entry.DN) == "" {
		return Principal{}, ErrAuthenticationUnavailable
	}
	if err := conn.Bind(entry.DN, password); err != nil {
		var ldapErr *ldap.Error
		if errors.As(err, &ldapErr) && ldapErr.ResultCode == ldap.LDAPResultInvalidCredentials {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, ErrAuthenticationUnavailable
	}
	p := Principal{Subject: "ldap:" + strings.ToLower(entry.DN), Provider: "ldap"}
	if cfg.GroupAttr != "" {
		for _, group := range entry.GetEqualFoldAttributeValues(cfg.GroupAttr) {
			p.Groups = append(p.Groups, "ldap:"+strings.ToLower(group))
		}
	}
	if err := p.Validate(); err != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	return p, nil
}
