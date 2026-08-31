package kubernetes

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type ReadinessConfig struct {
	APIURL    string
	TokenFile string
	CAFile    string
	K8sGPTNS  string
}

type ReadinessChecker struct {
	config ReadinessConfig
}

func NewReadinessChecker(config ReadinessConfig) *ReadinessChecker {
	return &ReadinessChecker{config: config}
}

func (c *ReadinessChecker) Ready(ctx context.Context) error {
	if strings.TrimSpace(c.config.APIURL) == "" {
		return fmt.Errorf("Kubernetes API URL is not configured")
	}

	tokenBytes, err := os.ReadFile(c.config.TokenFile)
	if err != nil {
		return fmt.Errorf("read service account token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return fmt.Errorf("service account token is empty")
	}

	caBytes, err := os.ReadFile(c.config.CAFile)
	if err != nil {
		return fmt.Errorf("read Kubernetes CA: %w", err)
	}
	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM(caBytes); !ok {
		return fmt.Errorf("parse Kubernetes CA")
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
			},
		},
	}
	defer client.CloseIdleConnections()

	endpoint := strings.TrimRight(c.config.APIURL, "/") +
		"/apis/core.k8sgpt.ai/v1alpha1/namespaces/" +
		url.PathEscape(c.config.K8sGPTNS) +
		"/results?limit=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build readiness request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("query Result API: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Result API returned HTTP %d", resp.StatusCode)
	}

	return nil
}
