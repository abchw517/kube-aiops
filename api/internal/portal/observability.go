package portal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode"

	"github.com/abchw517/kube-aiops/api/internal/alertmanager"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/observability"
	"github.com/abchw517/kube-aiops/api/internal/prometheus"
	"github.com/abchw517/kube-aiops/api/internal/victorialogs"
)

func configureObservability(store *identitystore.Store, managed *onboarding) *incident.ObservabilityIntegration {
	allowed := map[string]bool{}
	legacy := []string{}
	for _, pair := range [][2]string{{"prometheus", "PROMETHEUS"}, {"victorialogs", "VICTORIALOGS"}, {"alertmanager", "ALERTMANAGER"}, {"grafana", "GRAFANA"}} {
		if pair[0] != "grafana" && os.Getenv(pair[1]+"_URL") != "" {
			legacy = append(legacy, pair[0])
		}
		for _, raw := range strings.FieldsFunc(os.Getenv("DIAGNOSTIC_"+pair[1]+"_ENDPOINTS"), func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			if endpoint, err := observability.ManagedEndpoint(raw); err == nil {
				allowed[pair[0]+"\x00"+endpoint] = true
			}
		}
	}
	path := store.Path() + ".observability-credentials.key"
	aad := func(id, endpoint string) []byte {
		return []byte("kube-aiops/observability/v1\x00" + id + "\x00" + endpoint)
	}
	return &incident.ObservabilityIntegration{
		AlertNamespace: func(ctx context.Context, scope incident.ObservabilityScope, namespace string) (bool, error) {
			if namespace == "" {
				return true, nil
			}
			resolved, err := managed.resolveDiagnosticScope(ctx, incident.Scope{ClusterID: scope.Cluster, Namespace: namespace, EnvironmentID: scope.Environment})
			if errors.Is(err, incident.ErrBadScope) {
				return false, nil
			}
			return err == nil && resolved.BusinessID == scope.Business, err
		},
		Allowed: func(kind, endpoint string) bool { return allowed[kind+"\x00"+endpoint] },
		Legacy:  legacy,
		Seal: func(ctx context.Context, id, endpoint string, c observability.Credential) ([]byte, error) {
			key, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				var occupied bool
				if err = store.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM diagnostic_observability)").Scan(&occupied); err != nil {
					return nil, err
				}
				if occupied {
					key, err = os.ReadFile(path)
				} else {
					key, err = createClusterKey(path)
				}
			}
			if err != nil {
				return nil, err
			}
			cipher, err := newClusterCipher(key)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(c)
			if err != nil {
				return nil, err
			}
			return cipher.Seal(nil, nil, raw, aad(id, endpoint)), nil
		},
		Open: func(id, endpoint string, data []byte) (observability.Credential, error) {
			var c observability.Credential
			key, err := os.ReadFile(path)
			if err != nil {
				return c, err
			}
			cipher, err := newClusterCipher(key)
			if err != nil {
				return c, err
			}
			raw, err := cipher.Open(nil, nil, data, aad(id, endpoint))
			if err != nil {
				return c, err
			}
			err = json.Unmarshal(raw, &c)
			return c, err
		},
	}
}

func applyObservability(ctx context.Context, server *incident.Server, scope incident.Scope, base correlation.Correlator) (correlation.Correlator, error) {
	if server.Observability == nil || scope.BusinessID == "" || scope.EnvironmentID == "" {
		return base, nil
	}
	service, ok := base.(*correlation.Service)
	if !ok {
		return base, nil
	}
	var managed correlation.ServiceOptions
	for _, kind := range []string{"prometheus", "victorialogs", "alertmanager"} {
		config, found, err := server.ManagedObservability(ctx, kind, scope)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		switch kind {
		case "prometheus":
			managed.Prometheus, err = prometheus.New(config)
		case "victorialogs":
			managed.VictoriaLogs, err = victorialogs.New(config)
		case "alertmanager":
			managed.Alertmanager, err = alertmanager.New(config)
		}
		if err != nil {
			return nil, err
		}
	}
	return service.WithManagedSources(managed), nil
}
