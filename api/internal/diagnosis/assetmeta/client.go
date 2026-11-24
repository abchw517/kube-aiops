package assetmeta

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	Zabbix  = "zabbix"
	Aliyun  = "aliyun-ecs"
	Tencent = "tencent-cvm"
)

var (
	ErrConfiguration = errors.New("invalid connector configuration")
	ErrCredentials   = errors.New("connector credentials unavailable")
	ErrAuthorization = errors.New("connector authorization failed")
	ErrUnavailable   = errors.New("connector upstream unavailable")
)

type Config struct {
	Kind     string   `json:"kind"`
	Endpoint string   `json:"endpoint,omitempty"`
	Region   string   `json:"region,omitempty"`
	AssetIDs []string `json:"assetIds"`
}

type Asset struct {
	ExternalID string `json:"externalId"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Region     string `json:"region,omitempty"`
	State      string `json:"state"`
}

type Credentials struct {
	Scope           string   `json:"scope"`
	Kind            string   `json:"kind"`
	Endpoint        string   `json:"endpoint"`
	Region          string   `json:"region"`
	AssetIDs        []string `json:"assetIds"`
	Purpose         string   `json:"purpose"`
	Token           string   `json:"token"`
	AccessKeyID     string   `json:"accessKeyId"`
	AccessKeySecret string   `json:"accessKeySecret"`
	SessionToken    string   `json:"sessionToken"`
}

type Client struct {
	http      *http.Client
	hosts     map[string]bool
	directory string
}

func New(directory string, endpoints []string) *Client {
	allow := map[string]bool{}
	for _, v := range endpoints {
		if endpoint, err := normalizeZabbixEndpoint(v); err == nil {
			allow[endpoint] = true
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	return &Client{
		http:  &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		hosts: allow, directory: directory,
	}
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var regionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

func ValidReference(value string) bool { return identifier.MatchString(value) }

func (c *Client) Validate(config Config) error {
	if len(config.AssetIDs) == 0 || len(config.AssetIDs) > 50 {
		return ErrConfiguration
	}
	seen := map[string]bool{}
	for _, id := range config.AssetIDs {
		if !identifier.MatchString(id) || seen[id] {
			return ErrConfiguration
		}
		seen[id] = true
	}
	switch config.Kind {
	case Zabbix:
		if config.Region != "" {
			return ErrConfiguration
		}
		if _, err := c.ZabbixEndpoint(config.Endpoint); err != nil {
			return err
		}
	case Aliyun, Tencent:
		if !regionPattern.MatchString(config.Region) || config.Endpoint != "" {
			return ErrConfiguration
		}
	default:
		return ErrConfiguration
	}
	return nil
}

func (c *Client) ReadCredentials(reference string) (Credentials, error) {
	var value Credentials
	if !ValidReference(reference) || c.directory == "" {
		return value, ErrCredentials
	}
	root, err := os.OpenRoot(c.directory)
	if err != nil {
		return value, ErrCredentials
	}
	defer root.Close()
	file, err := root.Open(reference)
	if err != nil {
		return value, ErrCredentials
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
		return value, ErrCredentials
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &value) != nil {
		return Credentials{}, ErrCredentials
	}
	for _, s := range []string{value.Token, value.AccessKeyID, value.AccessKeySecret, value.SessionToken} {
		if len(s) > 4096 || strings.ContainsAny(s, "\r\n\x00") {
			return Credentials{}, ErrCredentials
		}
	}
	return value, nil
}

func (c *Client) boundCredentials(ref, scope, purpose string, cfg Config) (Credentials, error) {
	keys, err := c.ReadCredentials(ref)
	if err != nil || keys.Scope != scope || keys.Purpose != purpose || keys.Kind != cfg.Kind ||
		keys.Endpoint != cfg.Endpoint || keys.Region != cfg.Region {
		return Credentials{}, ErrCredentials
	}
	for _, id := range cfg.AssetIDs {
		if !slices.Contains(keys.AssetIDs, id) {
			return Credentials{}, ErrCredentials
		}
	}
	return keys, nil
}

func (c *Client) IngressToken(ref, scope string, cfg Config) (string, error) {
	keys, err := c.boundCredentials(ref, scope, "ingress", cfg)
	if err != nil || len(keys.Token) < 24 {
		return "", ErrCredentials
	}
	return keys.Token, nil
}

func (c *Client) Assets(ctx context.Context, cfg Config, ref, scope string) ([]Asset, error) {
	if err := c.Validate(cfg); err != nil {
		return nil, err
	}
	keys, err := c.boundCredentials(ref, scope, "inventory", cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Kind == Zabbix && keys.Token == "" || cfg.Kind != Zabbix && (keys.AccessKeyID == "" || keys.AccessKeySecret == "") {
		return nil, ErrCredentials
	}
	var assets []Asset
	switch cfg.Kind {
	case Zabbix:
		assets, err = c.zabbix(ctx, cfg, keys)
	case Aliyun:
		assets, err = c.aliyun(ctx, cfg, keys)
	case Tencent:
		assets, err = c.tencent(ctx, cfg, keys)
	}
	if err != nil {
		return nil, err
	}
	result := make([]Asset, 0, len(assets))
	seen := map[string]bool{}
	for _, a := range assets {
		if !slices.Contains(cfg.AssetIDs, a.ExternalID) || seen[a.ExternalID] {
			continue
		}
		seen[a.ExternalID] = true
		a.Region = cfg.Region
		result = append(result, a)
	}
	return result, nil
}

func (c *Client) request(req *http.Request, target any) error {
	response, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		var tlsErr *tls.CertificateVerificationError
		if errors.As(err, &tlsErr) {
			return ErrZabbixTLS
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return context.DeadlineExceeded
		}
		return ErrUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuthorization
	case http.StatusOK:
	default:
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, target) != nil {
		return ErrUnavailable
	}
	return nil
}

func jsonRequest(ctx context.Context, endpoint string, data []byte) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
}

func (c *Client) zabbix(ctx context.Context, cfg Config, keys Credentials) ([]Asset, error) {
	endpoint, err := c.ZabbixEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "host.get", "id": 1,
		"params": map[string]any{"hostids": cfg.AssetIDs, "output": []string{"hostid", "name", "status"}, "limit": 50}})
	req, err := jsonRequest(ctx, endpoint, body)
	if err != nil {
		return nil, ErrConfiguration
	}
	req.Header.Set("Content-Type", "application/json-rpc")
	req.Header.Set("Authorization", "Bearer "+keys.Token)
	var payload struct {
		Result []struct {
			ID     string `json:"hostid"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err = c.request(req, &payload); err != nil {
		return nil, err
	}
	if len(payload.Error) > 0 && string(payload.Error) != "null" {
		return nil, ErrAuthorization
	}
	if payload.Result == nil {
		return nil, ErrUnavailable
	}
	output := []Asset{}
	for _, h := range payload.Result {
		state := "disabled"
		if h.Status == "0" {
			state = "monitored"
		}
		output = append(output, Asset{ExternalID: h.ID, Name: h.Name, Kind: "Host", State: state})
	}
	return output, nil
}
