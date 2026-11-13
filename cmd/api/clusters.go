package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"github.com/abchw517/kube-aiops/internal/config"
	"github.com/abchw517/kube-aiops/internal/httpapi"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/observability"
)

type clusterConfig struct {
	ID, Name, Environment, APIURL, TokenFile, CAFile, K8sGPTNamespace, K8sGPTName string
	Prometheus, VictoriaLogs, Alertmanager                                        observability.Config
}

var clusterIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func loadClusters(path string, local httpapi.Backend, cfg config.Config) (httpapi.Backend, error) {
	if path == "" {
		return local, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	var configs []clusterConfig
	if err := decoder.Decode(&configs); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("invalid cluster configuration trailer")
	}
	if len(configs) > 0 {
		for _, source := range []observability.Config{cfg.Prometheus, cfg.VictoriaLogs, cfg.Alertmanager} {
			if source.URL != "" && source.ClusterLabelValue == "" {
				return nil, fmt.Errorf("local observability sources require a cluster label when remote clusters are configured")
			}
		}
	}
	registry := &httpapi.ClusterBackends{Backend: local, Targets: map[string]httpapi.ClusterTarget{}}
	for _, entry := range configs {
		endpoint, err := url.Parse(entry.APIURL)
		if !clusterIDPattern.MatchString(entry.ID) || entry.ID == "local" || err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") ||
			!filepath.IsAbs(entry.TokenFile) || !filepath.IsAbs(entry.CAFile) || entry.K8sGPTNamespace == "" || entry.K8sGPTName == "" {
			return nil, fmt.Errorf("invalid remote cluster configuration for %q", entry.ID)
		}
		if _, ok := registry.Targets[entry.ID]; ok {
			return nil, fmt.Errorf("duplicate cluster ID %q", entry.ID)
		}
		for _, source := range []observability.Config{entry.Prometheus, entry.VictoriaLogs, entry.Alertmanager} {
			if source.URL != "" && source.ClusterLabelValue == "" {
				return nil, fmt.Errorf("cluster %q: observability sources require a cluster label", entry.ID)
			}
		}
		backend := kubernetes.NewClient(kubernetes.Config{ClusterID: entry.ID, ClusterName: entry.Name, Environment: entry.Environment,
			APIURL: entry.APIURL, TokenFile: entry.TokenFile, CAFile: entry.CAFile, K8sGPTNS: entry.K8sGPTNamespace, K8sGPTName: entry.K8sGPTName})
		correlator, err := buildCorrelator(config.Config{Prometheus: entry.Prometheus, VictoriaLogs: entry.VictoriaLogs, Alertmanager: entry.Alertmanager}, backend)
		if err != nil {
			return nil, fmt.Errorf("cluster %q: %w", entry.ID, err)
		}
		registry.Targets[entry.ID] = httpapi.ClusterTarget{Backend: backend, Correlator: correlator}
	}
	return registry, nil
}
