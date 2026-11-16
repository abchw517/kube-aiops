package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/internal/config"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
)

func clusterReport() error {
	base, id := os.Getenv("CLUSTER_REPORT_URL"), os.Getenv("CLUSTER_REPORT_ID")
	if !remoteHTTPS(base) || !clusterIDPattern.MatchString(id) {
		return errors.New("cluster-report requires a trusted HTTPS CLUSTER_REPORT_URL and CLUSTER_REPORT_ID")
	}
	token, err := os.ReadFile(os.Getenv("CLUSTER_REPORT_TOKEN_FILE"))
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return errors.New("cannot read CLUSTER_REPORT_TOKEN_FILE")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client := kubernetes.NewClient(kubernetes.Config{ClusterID: id, APIURL: cfg.KubernetesAPIURL,
		TokenFile: cfg.KubernetesTokenFile, CAFile: cfg.KubernetesCAFile, K8sGPTNS: cfg.K8sGPTNamespace, K8sGPTName: cfg.K8sGPTName})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	captured := time.Now().UTC()
	items, err := client.SnapshotFindings(ctx)
	if err != nil {
		return err
	}
	for i, item := range items {
		safe, err := sanitizer.Default().Finding(item)
		if err != nil {
			return errors.New("snapshot contains an invalid Finding")
		}
		items[i] = safe
	}
	if items == nil {
		items = []finding.Finding{}
	}
	body, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return errors.New("snapshot exceeds 1 MiB")
	}
	endpoint := strings.TrimRight(base, "/") + "/api/v1/collector/snapshot?" + url.Values{"cluster": {id}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Snapshot-Captured-At", captured.Format(time.RFC3339Nano))
	httpClient := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("central API declined snapshot: HTTP %d", response.StatusCode)
	}
	return nil
}
