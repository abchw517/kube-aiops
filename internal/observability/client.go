package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode"
)

type Config struct {
	URL               string
	ClusterLabelValue string
	TokenFile         string
	CAFile            string
}

type Client struct {
	baseURL   *url.URL
	tokenFile string
	http      *http.Client
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
	return &Client{
		baseURL:   endpoint,
		tokenFile: config.TokenFile,
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) BearerToken() (string, error) {
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

	response, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("execute upstream query")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream query returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, errors.New("read bounded upstream response")
	}
	return body, nil
}
