package portal

import (
	"context"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"slices"

	"github.com/abchw517/kube-aiops/api/internal/wiki"
)

func (o *onboarding) wikiTarget(ctx context.Context, cluster, namespace string) (wiki.Target, error) {
	target, ok := o.scopedCluster(cluster)
	if !ok || namespace == "" {
		return wiki.Target{}, nil
	}
	env, err := o.clusterEnvironment(ctx, cluster)
	if err != nil || env == "" {
		return wiki.Target{}, err
	}
	namespaces, err := target.Backend.ListNamespaces(ctx)
	if err != nil {
		return wiki.Target{}, err
	}
	if !slices.ContainsFunc(namespaces, func(item kubernetes.Namespace) bool { return item.Name == namespace }) {
		return wiki.Target{}, nil
	}
	scope := wiki.Target{Cluster: cluster, Namespace: namespace, Environment: env}
	if env == "development" || env == "testing" {
		return scope, nil
	}
	rows, err := o.store.QueryContext(ctx, `SELECT DISTINCT n.business FROM namespace_business n
		JOIN resource_catalog_ids ids ON ids.kind='business-lines' AND ids.stable_key=n.business
		WHERE n.cluster=? AND n.namespace=? ORDER BY n.business`, cluster, namespace)
	if err != nil {
		return wiki.Target{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return wiki.Target{}, err
		}
		scope.Businesses = append(scope.Businesses, key)
	}
	if err := rows.Err(); err != nil {
		return wiki.Target{}, err
	}
	if len(scope.Businesses) == 0 {
		return wiki.Target{}, nil
	}
	return scope, nil
}
