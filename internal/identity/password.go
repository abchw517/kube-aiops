package identity

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
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
	UserUID      string
	SearchScope  string
	GroupAttr    string
	TLSConfig    *tls.Config
}

var ldapAttribute = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

func (cfg LDAPConfig) filterTemplate() string {
	filter := strings.TrimSpace(cfg.UserFilter)
	if cfg.UserUID != "" {
		uid := "(" + cfg.UserUID + "=%s)"
		if filter == "" {
			return uid
		}
		if !strings.HasPrefix(filter, "(") {
			filter = "(" + filter + ")"
		}
		return "(&" + uid + filter + ")"
	}
	if ldapAttribute.MatchString(filter) {
		return "(" + filter + "=%s)"
	}
	return filter
}

func (cfg LDAPConfig) Validate() error {
	endpoint, err := url.Parse(cfg.URL)
	if err != nil || (endpoint.Scheme != "ldaps" && endpoint.Scheme != "ldap") || endpoint.Hostname() == "" ||
		endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("LDAP URL 请使用 ldaps://域控域名:636 或 ldap://域控域名:389（StartTLS），且不能包含路径或凭据")
	}
	if _, err := ldap.ParseDN(cfg.BaseDN); err != nil || strings.TrimSpace(cfg.BaseDN) == "" {
		return errors.New("LDAP 基础 DN 无效")
	}
	if _, err := ldap.ParseDN(cfg.BindDN); err != nil || strings.TrimSpace(cfg.BindDN) == "" {
		return errors.New("LDAP 搜索 DN 无效")
	}
	if cfg.BindPassword == "" {
		return errors.New("请填写 LDAP 搜索密码")
	}
	if cfg.TLSConfig != nil && cfg.TLSConfig.InsecureSkipVerify {
		return errors.New("LDAP 必须验证 TLS 证书")
	}
	if cfg.UserUID != "" && !ldapAttribute.MatchString(cfg.UserUID) {
		return errors.New("LDAP 用户 UID 必须是有效的属性名，例如 sAMAccountName")
	}
	switch cfg.SearchScope {
	case "", "subtree", "onelevel", "base":
	default:
		return errors.New("LDAP 搜索范围无效")
	}
	filter := cfg.filterTemplate()
	if strings.Count(filter, "%s") != 1 || strings.Count(filter, "%") != 1 {
		return errors.New("LDAP 用户过滤器应填写属性名（如 sAMAccountName），或包含一个 %s 的完整过滤器")
	}
	if _, err := ldap.CompileFilter(fmt.Sprintf(filter, "validation")); err != nil {
		return errors.New("LDAP 用户过滤器格式无效，例如 (sAMAccountName=%s)")
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
	conn, closeConnection, err := dialLDAP(ctx, cfg)
	if err != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	defer closeConnection()
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	attributes := []string{"dn"}
	if cfg.GroupAttr != "" {
		attributes = append(attributes, cfg.GroupAttr)
	}
	search := ldap.NewSearchRequest(cfg.BaseDN, cfg.searchScope(), ldap.NeverDerefAliases,
		2, 5, false, fmt.Sprintf(cfg.filterTemplate(), ldap.EscapeFilter(username)), attributes, nil)
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
		if hasRangedGroups(entry, cfg.GroupAttr) {
			if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
				return Principal{}, ErrAuthenticationUnavailable
			}
		}
		groups, err := ldapGroups(conn, entry, cfg.GroupAttr)
		if err != nil {
			return Principal{}, ErrAuthenticationUnavailable
		}
		for _, group := range groups {
			p.Groups = append(p.Groups, "ldap:"+strings.ToLower(group))
		}
	}
	if err := p.Validate(); err != nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	return p, nil
}

func hasRangedGroups(entry *ldap.Entry, attr string) bool {
	prefix := strings.ToLower(attr) + ";range="
	for _, value := range entry.Attributes {
		if strings.HasPrefix(strings.ToLower(value.Name), prefix) {
			return true
		}
	}
	return false
}

func ldapGroups(conn *ldap.Conn, entry *ldap.Entry, attr string) ([]string, error) {
	if !hasRangedGroups(entry, attr) {
		return entry.GetEqualFoldAttributeValues(attr), nil
	}
	prefix := strings.ToLower(attr) + ";range="
	dn := entry.DN
	var groups []string
	for {
		rangePrefix := prefix + strconv.Itoa(len(groups)) + "-"
		var selected *ldap.EntryAttribute
		for _, candidate := range entry.Attributes {
			if strings.HasPrefix(strings.ToLower(candidate.Name), rangePrefix) {
				selected = candidate
				break
			}
		}
		if selected == nil || len(groups)+len(selected.Values) > maxGroups {
			return nil, ErrAuthenticationUnavailable
		}
		end := strings.TrimPrefix(strings.ToLower(selected.Name), rangePrefix)
		if end == "*" {
			return append(groups, selected.Values...), nil
		}
		last, err := strconv.Atoi(end)
		if err != nil || len(selected.Values) == 0 || last != len(groups)+len(selected.Values)-1 {
			return nil, ErrAuthenticationUnavailable
		}
		groups = append(groups, selected.Values...)
		if len(groups) >= maxGroups {
			return nil, ErrAuthenticationUnavailable
		}
		next := fmt.Sprintf("%s;range=%d-*", attr, len(groups))
		result, err := conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
			1, 5, false, "(objectClass=*)", []string{next}, nil))
		if err != nil || len(result.Entries) != 1 {
			return nil, ErrAuthenticationUnavailable
		}
		entry = result.Entries[0]
	}
}

func (cfg LDAPConfig) searchScope() int {
	switch cfg.SearchScope {
	case "base":
		return ldap.ScopeBaseObject
	case "onelevel":
		return ldap.ScopeSingleLevel
	default:
		return ldap.ScopeWholeSubtree
	}
}

func dialLDAP(ctx context.Context, cfg LDAPConfig) (*ldap.Conn, func(), error) {
	endpoint, _ := url.Parse(cfg.URL)
	port := endpoint.Port()
	if port == "" {
		port = "636"
		if endpoint.Scheme == "ldap" {
			port = "389"
		}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSConfig != nil {
		tlsConfig = cfg.TLSConfig.Clone()
		tlsConfig.MinVersion = max(tlsConfig.MinVersion, tls.VersionTLS12)
	}
	tlsConfig.ServerName = endpoint.Hostname()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)

	address := net.JoinHostPort(endpoint.Hostname(), port)
	var socket net.Conn
	var err error
	if endpoint.Scheme == "ldap" {
		socket, err = (&net.Dialer{}).DialContext(ctx, "tcp", address)
	} else {
		socket, err = (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", address)
	}
	if err != nil {
		cancel()
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = socket.Close() })
	conn := ldap.NewConn(socket, endpoint.Scheme == "ldaps")
	conn.SetTimeout(5 * time.Second)
	conn.Start()
	if endpoint.Scheme == "ldap" {
		if err := conn.StartTLS(tlsConfig); err != nil {
			stop()
			_ = socket.Close()
			conn.Close()
			cancel()
			return nil, nil, err
		}
	}
	return conn, func() { stop(); conn.Close(); cancel() }, nil
}

// TestLDAP checks TLS, service-account bind and base DN search without writing settings.
func TestLDAP(ctx context.Context, cfg LDAPConfig) string {
	if cfg.Validate() != nil {
		return "configuration"
	}
	conn, closeConnection, err := dialLDAP(ctx, cfg)
	if err != nil {
		return "connection"
	}
	defer closeConnection()
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return "bind"
	}
	result, err := conn.Search(ldap.NewSearchRequest(cfg.BaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		1, 5, false, "(objectClass=*)", []string{"1.1"}, nil))
	if err != nil || len(result.Entries) != 1 {
		return "search"
	}
	return ""
}
