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
	GroupBaseDN  string
	GroupFilter  string
	GroupScope   string
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
	if cfg.GroupBaseDN != "" {
		if _, err := ldap.ParseDN(cfg.GroupBaseDN); err != nil {
			return errors.New("LDAP 组基础 DN 无效")
		}
	}
	if (cfg.GroupBaseDN != "" || cfg.GroupFilter != "" || cfg.GroupScope != "") && cfg.GroupAttr == "" {
		return errors.New("请填写 LDAP 组成员属性，例如 memberOf")
	}
	if (cfg.GroupFilter != "" || cfg.GroupScope != "") && cfg.GroupBaseDN == "" {
		return errors.New("配置组过滤器或范围时，请填写 LDAP 组基础 DN")
	}
	switch cfg.GroupScope {
	case "", "subtree", "onelevel", "base":
	default:
		return errors.New("LDAP 组搜索范围无效")
	}
	if cfg.GroupFilter != "" {
		if _, err := ldap.CompileFilter(parenthesizedFilter(cfg.GroupFilter)); err != nil {
			return errors.New("LDAP 组过滤器无效，例如 (objectClass=group)")
		}
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
	principal, _, err := probeLDAPLogin(ctx, cfg, username, password)
	return principal, err
}

// VerifyLDAPLogin probes the same directory operations used by a real login
// without registering an account or issuing a session.
func VerifyLDAPLogin(ctx context.Context, cfg LDAPConfig, username, password string) string {
	_, stage, _ := probeLDAPLogin(ctx, cfg, username, password)
	return stage
}

func probeLDAPLogin(ctx context.Context, cfg LDAPConfig, username, password string) (Principal, string, error) {
	if username == "" || password == "" {
		return Principal{}, "credentials_required", ErrUnauthenticated
	}
	if cfg.Validate() != nil {
		return Principal{}, "configuration", ErrAuthenticationUnavailable
	}
	conn, closeConnection, stage := dialLDAP(ctx, cfg)
	if stage != "" {
		return Principal{}, stage, ErrAuthenticationUnavailable
	}
	defer closeConnection()
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return Principal{}, "bind", ErrAuthenticationUnavailable
	}
	entry, stage := cfg.searchUser(conn, username)
	if stage == "user_match" {
		return Principal{}, stage, ErrUnauthenticated
	}
	if stage != "" {
		return Principal{}, stage, ErrAuthenticationUnavailable
	}
	if directoryEntryDisabled(entry, time.Now()) {
		return Principal{}, "user_auth", ErrUnauthenticated
	}
	if err := conn.Bind(entry.DN, password); err != nil {
		var ldapErr *ldap.Error
		if errors.As(err, &ldapErr) && ldapErr.ResultCode == ldap.LDAPResultInvalidCredentials {
			return Principal{}, "user_auth", ErrUnauthenticated
		}
		return Principal{}, "user_auth", ErrAuthenticationUnavailable
	}
	if cfg.GroupAttr != "" && (hasRangedGroups(entry, cfg.GroupAttr) || cfg.GroupFilter != "") {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return Principal{}, "group_query", ErrAuthenticationUnavailable
		}
	}
	principal, stage := cfg.directoryPrincipal(conn, entry)
	if stage != "" {
		return Principal{}, stage, ErrAuthenticationUnavailable
	}
	return principal, "success", nil
}

func (cfg LDAPConfig) searchUser(conn *ldap.Conn, username string) (*ldap.Entry, string) {
	attributes := []string{"dn", "userAccountControl", "pwdAccountLockedTime", "nsAccountLock", "shadowExpire"}
	if cfg.GroupAttr != "" {
		attributes = append(attributes, cfg.GroupAttr)
	}
	entry, stage := cfg.searchDirectoryUser(conn, username, attributes)
	switch stage {
	case "success":
		return entry, ""
	case "user_absent":
		return nil, "user_match"
	default:
		return nil, stage
	}
}

func (cfg LDAPConfig) searchDirectoryUser(conn *ldap.Conn, username string, attributes []string) (*ldap.Entry, string) {
	search := ldap.NewSearchRequest(cfg.BaseDN, cfg.searchScope(), ldap.NeverDerefAliases,
		2, 5, false, fmt.Sprintf(cfg.filterTemplate(), ldap.EscapeFilter(username)), attributes, nil)
	result, err := conn.Search(search)
	if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return nil, "user_match"
	}
	if err != nil {
		return nil, "user_search"
	}
	if len(result.Entries) == 0 {
		return nil, "user_absent"
	}
	if len(result.Entries) != 1 {
		return nil, "user_match"
	}
	entry := result.Entries[0]
	if strings.TrimSpace(entry.DN) == "" {
		return nil, "user_search"
	}
	return entry, "success"
}

func (cfg LDAPConfig) directoryPrincipal(conn *ldap.Conn, entry *ldap.Entry) (Principal, string) {
	principal := Principal{Subject: "ldap:" + strings.ToLower(entry.DN), Provider: "ldap"}
	if cfg.GroupAttr != "" {
		groups, err := ldapGroups(conn, entry, cfg.GroupAttr)
		if err != nil {
			return Principal{}, "group_query"
		}
		groups, err = cfg.filterGroups(conn, groups)
		if err != nil {
			return Principal{}, "group_query"
		}
		for _, group := range groups {
			principal.Groups = append(principal.Groups, "ldap:"+strings.ToLower(group))
		}
	}
	if err := principal.Validate(); err != nil {
		return Principal{}, "identity_mapping"
	}
	return principal, ""
}

func parenthesizedFilter(value string) string {
	if strings.HasPrefix(value, "(") {
		return value
	}
	return "(" + value + ")"
}

func (cfg LDAPConfig) filterGroups(conn *ldap.Conn, groups []string) ([]string, error) {
	if cfg.GroupBaseDN == "" {
		return groups, nil
	}
	base, err := ldap.ParseDN(cfg.GroupBaseDN)
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(groups))
	for _, group := range groups {
		dn, err := ldap.ParseDN(group)
		if err != nil {
			return nil, err
		}
		inScope := base.EqualFold(dn) || base.AncestorOfFold(dn)
		switch cfg.GroupScope {
		case "base":
			inScope = base.EqualFold(dn)
		case "onelevel":
			inScope = base.AncestorOfFold(dn) && len(dn.RDNs) == len(base.RDNs)+1
		}
		if !inScope {
			continue
		}
		if cfg.GroupFilter != "" {
			result, err := conn.Search(ldap.NewSearchRequest(group, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
				1, 5, false, parenthesizedFilter(cfg.GroupFilter), []string{"1.1"}, nil))
			if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if len(result.Entries) == 0 {
				continue
			}
		}
		selected = append(selected, group)
	}
	return selected, nil
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

func dialLDAP(ctx context.Context, cfg LDAPConfig) (*ldap.Conn, func(), string) {
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
	socket, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		cancel()
		var dns *net.DNSError
		if errors.As(err, &dns) {
			return nil, nil, "connection_dns"
		}
		return nil, nil, "connection_tcp"
	}
	raw := socket
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	if endpoint.Scheme == "ldaps" {
		encrypted := tls.Client(socket, tlsConfig)
		if err := encrypted.HandshakeContext(ctx); err != nil {
			stop()
			_ = socket.Close()
			cancel()
			var certificate *tls.CertificateVerificationError
			if errors.As(err, &certificate) {
				return nil, nil, "connection_certificate"
			}
			return nil, nil, "connection_ldaps"
		}
		socket = encrypted
	}
	conn := ldap.NewConn(socket, endpoint.Scheme == "ldaps")
	conn.SetTimeout(5 * time.Second)
	conn.Start()
	if endpoint.Scheme == "ldap" {
		if err := conn.StartTLS(tlsConfig); err != nil {
			stop()
			_ = socket.Close()
			conn.Close()
			cancel()
			return nil, nil, "connection_starttls"
		}
	}
	return conn, func() { stop(); conn.Close(); cancel() }, ""
}

// TestLDAP checks TLS, service-account bind and base DN search. If a username
// is supplied, it also uses the same directory queries as login.
func TestLDAP(ctx context.Context, cfg LDAPConfig, username string) (stage string, groupCount int) {
	if cfg.Validate() != nil {
		return "configuration", 0
	}
	conn, closeConnection, connectionStage := dialLDAP(ctx, cfg)
	if connectionStage != "" {
		return connectionStage, 0
	}
	defer closeConnection()
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return "bind", 0
	}
	result, err := conn.Search(ldap.NewSearchRequest(cfg.BaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		1, 5, false, "(objectClass=*)", []string{"1.1"}, nil))
	if err != nil || len(result.Entries) != 1 {
		return "search", 0
	}
	if cfg.GroupBaseDN != "" {
		result, err := conn.Search(ldap.NewSearchRequest(cfg.GroupBaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
			1, 5, false, "(objectClass=*)", []string{"1.1"}, nil))
		if err != nil || len(result.Entries) != 1 {
			return "group_search", 0
		}
	}
	if username == "" {
		return "success", 0
	}
	entry, stage := cfg.searchUser(conn, username)
	if stage != "" {
		return stage, 0
	}
	principal, stage := cfg.directoryPrincipal(conn, entry)
	if stage != "" {
		return stage, 0
	}
	return "success_user", len(principal.Groups)
}
