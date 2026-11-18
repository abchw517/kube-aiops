package portal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

func (o *onboarding) scopedCluster(id string) (httpapi.ClusterTarget, bool) {
	if o.registry == nil {
		return httpapi.ClusterTarget{}, false
	}
	if id == "local" && o.registry.Backend != nil {
		return httpapi.ClusterTarget{Backend: o.registry.Backend}, true
	}
	return o.registry.Lookup(id)
}

func (o *onboarding) clusterEnvironment(ctx context.Context, id string) (string, error) {
	target, ok := o.scopedCluster(id)
	if !ok {
		return "", nil
	}
	for _, cluster := range target.Backend.Clusters() {
		if cluster.ID != id {
			continue
		}
		label := cluster.Environment
		var env string
		err := o.store.QueryRowContext(ctx, `SELECT COALESCE((SELECT stable_key FROM resource_catalog_ids WHERE kind='environments' AND alias=resource_catalog.id),id)
			FROM resource_catalog WHERE kind='environments' AND
			(id=? OR id IN (SELECT id FROM resource_catalog_names WHERE kind='environments' AND name=?)
			OR id IN (SELECT id FROM resource_catalog_ids WHERE kind='environments' AND alias=?))
			ORDER BY id=? DESC LIMIT 1`, label, label, label, label).Scan(&env)
		if errors.Is(err, sql.ErrNoRows) {
			switch strings.ToLower(label) {
			case "dev", "test":
				want := "development"
				if strings.EqualFold(label, "test") {
					want = "testing"
				}
				err = o.store.QueryRowContext(ctx, "SELECT stable_key FROM resource_catalog_ids WHERE kind='environments' AND alias=?", want).Scan(&env)
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return env, err
	}
	return "", nil
}

type catalogQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func catalogStableKey(ctx context.Context, db catalogQueryer, kind, label string) (string, error) {
	var key string
	err := db.QueryRowContext(ctx, `SELECT stable_key FROM resource_catalog_ids
			WHERE kind=? AND (alias=? OR alias=(SELECT id FROM resource_catalog_names WHERE kind=? AND name=?))
			ORDER BY alias=? DESC LIMIT 1`, kind, label, kind, label, label).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return key, err
}

func (o *onboarding) namespaceBusiness(ctx context.Context, cluster, namespace, business string) (bool, error) {
	var allowed bool
	err := o.store.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM namespace_business WHERE cluster=? AND namespace=? AND
		business=(SELECT stable_key FROM resource_catalog_ids WHERE kind='business-lines' AND alias=?))`, cluster, namespace, business).Scan(&allowed)
	return allowed, err
}

func (o *onboarding) namespaceAssignments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cluster := r.URL.Query().Get("cluster")
	target, ok := o.scopedCluster(cluster)
	if !ok {
		http.Error(w, "集群不可用", 404)
		return
	}
	if r.Method == http.MethodGet {
		namespaces, err := target.Backend.ListNamespaces(ctx)
		if err != nil {
			http.Error(w, "命名空间不可用", 503)
			return
		}
		rows, err := o.store.QueryContext(ctx, `SELECT n.namespace,b.id FROM namespace_business n JOIN resource_catalog_ids b
			ON b.kind='business-lines' AND b.alias=b.id AND b.stable_key=n.business
			WHERE n.cluster=? ORDER BY b.id`, cluster)
		if err != nil {
			http.Error(w, "归属暂不可用", 503)
			return
		}
		defer rows.Close()
		assigned := map[string][]string{}
		for rows.Next() {
			var ns, business string
			if err := rows.Scan(&ns, &business); err != nil {
				http.Error(w, "归属暂不可用", 503)
				return
			}
			assigned[ns] = append(assigned[ns], business)
		}
		if rows.Err() != nil {
			http.Error(w, "归属暂不可用", 503)
			return
		}
		adminhttp.WriteJSON(w, map[string]any{"namespaces": namespaces, "assigned": assigned})
		return
	}
	if r.Method != http.MethodPut {
		w.WriteHeader(405)
		return
	}
	var input struct {
		Namespace  string
		Businesses []string
	}
	if !adminhttp.Decode(w, r, &input) || input.Namespace == "" || len(input.Businesses) > 50 {
		http.Error(w, "归属无效", 400)
		return
	}
	namespaces, err := target.Backend.ListNamespaces(ctx)
	if err != nil {
		http.Error(w, "命名空间不可用", 503)
		return
	}
	if !slices.ContainsFunc(namespaces, func(ns kubernetes.Namespace) bool { return ns.Name == input.Namespace }) {
		http.Error(w, "命名空间不存在", 404)
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if current, valid := o.scopedCluster(cluster); !valid || current.Backend != target.Backend {
		http.Error(w, "集群配置已更新，请重试", 409)
		return
	}
	tx, err := o.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "保存失败", 503)
		return
	}
	defer tx.Rollback()
	directory, _ := target.Backend.(directoryBackend)
	dedicatedBusiness := ""
	if directory.spec.ClusterType == "dedicated" {
		dedicatedBusiness, err = catalogStableKey(ctx, tx, businessKind, directory.spec.Business)
		if err != nil || dedicatedBusiness == "" {
			http.Error(w, "独立业务集群归属不可用", 409)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM namespace_business WHERE cluster=? AND namespace=?", cluster, input.Namespace); err != nil {
		http.Error(w, "保存失败", 503)
		return
	}
	for _, id := range input.Businesses {
		var key string
		err := tx.QueryRowContext(ctx, "SELECT stable_key FROM resource_catalog_ids WHERE kind='business-lines' AND alias=? AND id=?", id, id).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "业务线不存在", 409)
			return
		}
		if err != nil {
			http.Error(w, "保存失败", 503)
			return
		}
		if dedicatedBusiness != "" && key != dedicatedBusiness {
			http.Error(w, "独立业务集群只能分配到所属业务线", 409)
			return
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO namespace_business VALUES(?,?,?)", cluster, input.Namespace, key); err != nil {
			http.Error(w, "保存失败", 503)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "保存失败", 503)
		return
	}
	w.WriteHeader(204)
}
