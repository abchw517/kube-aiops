package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const (
	defaultHTTPAddr       = ":8080"
	defaultReadyTimeout   = 3 * time.Second
	defaultK8sGPTNS       = "k8sgpt-operator-system"
	defaultK8sGPTName     = "k8sgpt-engine"
	defaultSATokenFile    = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultServiceCAFile  = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	defaultKubernetesPort = "443"
)

type Config struct {
	HTTPAddr            string
	ReadyTimeout        time.Duration
	K8sGPTNamespace     string
	K8sGPTName          string
	KubernetesAPIURL    string
	KubernetesTokenFile string
	KubernetesCAFile    string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:            getenv("HTTP_ADDR", defaultHTTPAddr),
		K8sGPTNamespace:     getenv("K8SGPT_NAMESPACE", defaultK8sGPTNS),
		K8sGPTName:          getenv("K8SGPT_NAME", defaultK8sGPTName),
		KubernetesAPIURL:    strings.TrimRight(os.Getenv("KUBERNETES_API_URL"), "/"),
		KubernetesTokenFile: getenv("KUBERNETES_BEARER_TOKEN_FILE", defaultSATokenFile),
		KubernetesCAFile:    getenv("KUBERNETES_CA_FILE", defaultServiceCAFile),
		ReadyTimeout:        defaultReadyTimeout,
	}

	if value := strings.TrimSpace(os.Getenv("READY_TIMEOUT")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse READY_TIMEOUT: %w", err)
		}
		if duration <= 0 {
			return Config{}, fmt.Errorf("READY_TIMEOUT must be greater than zero")
		}
		cfg.ReadyTimeout = duration
	}

	if cfg.KubernetesAPIURL == "" {
		cfg.KubernetesAPIURL = inClusterAPIURL()
	}

	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return Config{}, fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if strings.TrimSpace(cfg.K8sGPTNamespace) == "" {
		return Config{}, fmt.Errorf("K8SGPT_NAMESPACE must not be empty")
	}
	if strings.TrimSpace(cfg.K8sGPTName) == "" {
		return Config{}, fmt.Errorf("K8SGPT_NAME must not be empty")
	}

	return cfg, nil
}

func inClusterAPIURL() string {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	if host == "" {
		return ""
	}

	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS"))
	if port == "" {
		port = strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	}
	if port == "" {
		port = defaultKubernetesPort
	}

	return "https://" + net.JoinHostPort(host, port)
}

func getenv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
