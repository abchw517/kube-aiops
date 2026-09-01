package kubernetes

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const localClusterID = "local"

type Config struct {
	APIURL    string
	TokenFile string
	CAFile    string
	K8sGPTNS  string
}

type Client struct {
	config Config
}

type APIError struct {
	StatusCode int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Kubernetes API returned HTTP %d", e.StatusCode)
}

type Cluster struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Namespace struct {
	Name string `json:"name"`
}

type ResourceStatus struct {
	Phase             string `json:"phase,omitempty"`
	Replicas          *int64 `json:"replicas,omitempty"`
	ReadyReplicas     *int64 `json:"readyReplicas,omitempty"`
	AvailableReplicas *int64 `json:"availableReplicas,omitempty"`
}

type ResourceDetail struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace"`
	Name       string         `json:"name"`
	CreatedAt  string         `json:"createdAt,omitempty"`
	Status     ResourceStatus `json:"status"`
}

func NewClient(config Config) *Client {
	return &Client{config: config}
}

func (c *Client) Clusters() []Cluster {
	return []Cluster{{ID: localClusterID, Name: localClusterID, Status: "ready"}}
}

func (c *Client) Ready(ctx context.Context) error {
	var payload map[string]any
	path := "/apis/core.k8sgpt.ai/v1alpha1/namespaces/" +
		url.PathEscape(c.config.K8sGPTNS) + "/results?limit=1"
	return c.getJSON(ctx, path, &payload)
}

func (c *Client) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	var payload struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, "/api/v1/namespaces", &payload); err != nil {
		return nil, err
	}

	items := make([]Namespace, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.Metadata.Name == "" {
			continue
		}
		items = append(items, Namespace{Name: item.Metadata.Name})
	}
	return items, nil
}

func (c *Client) GetResource(ctx context.Context, kind, namespace, name string) (ResourceDetail, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if namespace == "" || name == "" {
		return ResourceDetail{}, fmt.Errorf("namespace and name are required")
	}

	var endpoint string
	switch kind {
	case "pod", "pods":
		endpoint = "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(name)
	case "deployment", "deployments":
		endpoint = "/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/deployments/" + url.PathEscape(name)
	default:
		return ResourceDetail{}, fmt.Errorf("unsupported resource kind: %s", kind)
	}

	var payload struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name              string `json:"name"`
			Namespace         string `json:"namespace"`
			CreationTimestamp string `json:"creationTimestamp"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			Replicas          *int64 `json:"replicas"`
			ReadyReplicas     *int64 `json:"readyReplicas"`
			AvailableReplicas *int64 `json:"availableReplicas"`
		} `json:"status"`
	}
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		return ResourceDetail{}, err
	}

	return ResourceDetail{
		APIVersion: payload.APIVersion,
		Kind:       payload.Kind,
		Namespace:  payload.Metadata.Namespace,
		Name:       payload.Metadata.Name,
		CreatedAt:  payload.Metadata.CreationTimestamp,
		Status: ResourceStatus{
			Phase:             payload.Status.Phase,
			Replicas:          payload.Status.Replicas,
			ReadyReplicas:     payload.Status.ReadyReplicas,
			AvailableReplicas: payload.Status.AvailableReplicas,
		},
	}, nil
}

func (c *Client) getJSON(ctx context.Context, path string, target any) error {
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

	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}}}
	defer httpClient.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.config.APIURL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("build Kubernetes request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("query Kubernetes API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return &APIError{StatusCode: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode Kubernetes response: %w", err)
	}
	return nil
}
