package portal

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func configureZabbix(store *identitystore.Store, admin *permissions.Admin, client *assetmeta.Client, o *onboarding, enabled func(string) bool) *incident.ZabbixIntegration {
	keyPath := store.Path() + ".zabbix-credentials.key"
	associated := func(id, endpoint string) []byte {
		value, _ := json.Marshal([3]string{"kube-aiops/zabbix/v1", id, endpoint})
		return value
	}
	active := func(ctx context.Context, s incident.ZabbixScope) (bool, error) {
		if !enabled(s.Environment) {
			return false, nil
		}
		var exists bool
		err := store.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM resource_catalog WHERE kind='environments' AND id=?)
			AND EXISTS(SELECT 1 FROM resource_catalog WHERE kind='business-lines' AND id=?)`, s.Environment, s.Business).Scan(&exists)
		return exists, err
	}
	return &incident.ZabbixIntegration{
		Collector:   client,
		ScopeActive: active,
		ClusterBinding: func(ctx context.Context, scope incident.ZabbixScope, id string) (bool, error) {
			environment, err := o.clusterEnvironment(ctx, id)
			if err != nil || environment != scope.Environment {
				return false, err
			}
			target, ok := o.scopedCluster(id)
			if !ok {
				return false, nil
			}
			for _, cluster := range target.Backend.Clusters() {
				if cluster.ID != id || cluster.Business == "" {
					continue
				}
				business, err := catalogStableKey(ctx, store, businessKind, cluster.Business)
				return business != "" && business == scope.Business, err
			}
			return false, nil
		},
		Authorize: func(ctx context.Context, p identity.Principal, code string, s incident.ZabbixScope) (bool, error) {
			if ok, err := active(ctx, s); err != nil || !ok {
				return false, err
			}
			return admin.AuthorizeHostDatasource(ctx, p, code, s.Environment, s.Business)
		},
		Catalog: func(ctx context.Context) (incident.ZabbixScopeOptions, error) {
			convert := func(kind string) ([]incident.ZabbixOption, error) {
				entries, err := readCatalog(ctx, store, kind)
				out := make([]incident.ZabbixOption, 0, len(entries))
				for _, entry := range entries {
					out = append(out, incident.ZabbixOption{ID: entry.ID, Name: entry.Name})
				}
				return out, err
			}
			env, err := convert(environmentKind)
			if err != nil {
				return incident.ZabbixScopeOptions{}, err
			}
			business, err := convert(businessKind)
			return incident.ZabbixScopeOptions{Environments: env, Businesses: business}, err
		},
		Seal: func(ctx context.Context, id, endpoint string, c assetmeta.ZabbixCredential) ([]byte, error) {
			key, err := os.ReadFile(keyPath)
			if errors.Is(err, os.ErrNotExist) {
				var occupied bool
				if err = store.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM diagnostic_zabbix_source)").Scan(&occupied); err != nil {
					return nil, err
				}
				if occupied {
					key, err = os.ReadFile(keyPath)
				} else {
					key, err = createClusterKey(keyPath)
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
			return cipher.Seal(nil, nil, raw, associated(id, endpoint)), nil
		},
		Open: func(id, endpoint string, encrypted []byte) (assetmeta.ZabbixCredential, error) {
			var c assetmeta.ZabbixCredential
			key, err := os.ReadFile(keyPath)
			if err != nil {
				return c, assetmeta.ErrCredentials
			}
			cipher, err := newClusterCipher(key)
			if err != nil {
				return c, assetmeta.ErrCredentials
			}
			raw, err := cipher.Open(nil, nil, encrypted, associated(id, endpoint))
			if err != nil || json.Unmarshal(raw, &c) != nil || !c.Valid() {
				return assetmeta.ZabbixCredential{}, assetmeta.ErrCredentials
			}
			return c, nil
		},
	}
}
