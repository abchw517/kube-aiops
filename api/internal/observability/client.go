package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"unicode"
)

var (
	ErrUpstreamPermission = errors.New("upstream permission denied")
	ErrUpstreamTimeout    = errors.New("upstream connection timed out")
	ErrUpstreamTLS        = errors.New("upstream certificate validation failed")
	managedTransport      = func() *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = dialManaged
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		return transport
	}()
)

type Config struct {
	URL               string
	ClusterLabelValue string
	TokenFile         string
	CAFile            string
	ClusterLabelKey   string
	Credential        Credential
	Tenant            *Tenant
	Managed           bool
}

type Credential struct {
	Method   string `json:"method"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

type Tenant struct {
	Account uint32 `json:"account"`
	Project uint32 `json:"project"`
}

type Client struct {
	baseURL    *url.URL
	tokenFile  string
	credential Credential
	tenant     *Tenant
	http       *http.Client
}

func NewClient(prefix string, config Config) (*Client, error) {
	if config.URL == "" {
		if config.ClusterLabelValue != "" || config.TokenFile != "" || config.CAFile != "" {
			return nil, fmt.Errorf("backend settings require %s_URL", prefix)
		}
		return nil, nil
	}

	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.ForceQuery || strings.Contains(config.URL, "#") || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return nil, fmt.Errorf("%s_URL must be an HTTP(S) base URL", prefix)
	}
	if endpoint.Scheme != "https" && (config.TokenFile != "" || config.CAFile != "") {
		return nil, fmt.Errorf("%s TLS settings require an HTTPS endpoint", prefix)
	}
	if len(config.ClusterLabelValue) > 253 || strings.IndexFunc(config.ClusterLabelValue, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("invalid %s_CLUSTER_LABEL_VALUE", prefix)
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.CAFile != "" {
		ca, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read %s CA file", prefix)
		}
		tlsConfig.RootCAs = x509.NewCertPool()
		if !tlsConfig.RootCAs.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("parse %s CA file", prefix)
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	if config.Managed {
		transport = managedTransport
	}
	return &Client{
		baseURL:    endpoint,
		tokenFile:  config.TokenFile,
		credential: config.Credential,
		tenant:     config.Tenant,
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) BearerToken() (string, error) {
	if c.credential.Method == "bearer" {
		return c.credential.Secret, nil
	}
	if c.tokenFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(c.tokenFile)
	token := strings.TrimSpace(string(data))
	if err != nil || token == "" || len(token) > 8192 || strings.IndexFunc(token, func(r rune) bool {
		return r < '!' || r > '~'
	}) >= 0 {
		return "", errors.New("read upstream bearer token")
	}
	return token, nil
}

func (c *Client) Get(ctx context.Context, path string, params url.Values, token string, limit int64) ([]byte, error) {
	endpoint := c.baseURL.JoinPath(path)
	endpoint.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("build upstream query")
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if c.credential.Method == "basic" {
		req.SetBasicAuth(c.credential.Username, c.credential.Secret)
	}
	if c.tenant != nil {
		req.Header.Set("AccountID", fmt.Sprint(c.tenant.Account))
		req.Header.Set("ProjectID", fmt.Sprint(c.tenant.Project))
	}
	response, err := c.http.Do(req)
	if err != nil {
		var certificate *tls.CertificateVerificationError
		var timeout net.Error
		if errors.As(err, &certificate) {
			return nil, ErrUpstreamTLS
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
			return nil, ErrUpstreamTimeout
		}
		return nil, errors.New("execute upstream query")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ErrUpstreamPermission
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream query returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, ErrUpstreamTimeout
	}
	if err != nil || int64(len(body)) > limit {
		return nil, errors.New("read bounded upstream response")
	}
	return body, nil
}

// ManagedEndpoint canonicalizes a configured HTTPS base URL before exact allowlist comparison.
func ManagedEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return "", errors.New("invalid HTTPS base URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path != "" && (path.Clean(u.Path) != u.Path || strings.ContainsAny(u.Path, "%\"\\") || strings.IndexFunc(u.Path, unicode.IsSpace) >= 0) {
		return "", errors.New("invalid HTTPS base URL")
	}
	return u.String(), nil
}

func dialManaged(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	var lastErr error = errors.New("upstream denied by network policy")
	for _, resolved := range addresses {
		ip := netip.Addr.Unmap(resolved)
		if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
