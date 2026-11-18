package portal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

func (o *onboarding) initLocal(ctx context.Context, client *kubernetes.Client) error {
	o.local = client
	cluster := client.Clusters()[0]
	spec := managedCluster{ID: "local", Name: cluster.Name, Environment: cluster.Environment, Mode: "local", Enabled: true, Generation: rand.Text()}
	data, _ := json.Marshal(spec)
	if _, err := o.store.ExecContext(ctx, "INSERT OR IGNORE INTO managed_clusters(id,document,token_hash) VALUES ('local',?,'')", string(data)); err != nil {
		return err
	}
	saved, err := o.clusterSpec(ctx, "local")
	if err != nil {
		return err
	}
	o.publishLocal(saved)
	return nil
}

func (o *onboarding) publishLocal(spec managedCluster) {
	target := httpapi.ClusterTarget{}
	if !spec.Deleted && spec.Enabled {
		cluster := o.local.Clusters()[0]
		cluster.Name, cluster.Environment, cluster.Business, cluster.Group = spec.Name, spec.Environment, spec.Business, spec.Group
		target.Backend = directoryBackend{Backend: o.registry.Backend, cluster: cluster, spec: spec}
	}
	o.registry.SetLocal(target)
}

func (o *onboarding) clusterSpec(ctx context.Context, id string) (managedCluster, error) {
	var data []byte
	var spec managedCluster
	if err := o.store.QueryRowContext(ctx, "SELECT document FROM managed_clusters WHERE id=?", id).Scan(&data); err != nil {
		return spec, err
	}
	err := json.Unmarshal(data, &spec)
	return spec, err
}

func (o *onboarding) editLocal(w http.ResponseWriter, r *http.Request, input managedCluster) {
	ctx := r.Context()
	if r.Method != http.MethodPut || r.URL.Query().Get("test") != "" || input.ID != "local" || o.local == nil {
		http.Error(w, "本地集群只支持编辑归属", 400)
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	current, err := o.clusterSpec(ctx, "local")
	if err != nil || current.Deleted {
		http.Error(w, "本地连接不可编辑", 409)
		return
	}
	for _, field := range []string{input.Name, input.Environment, input.Business, input.Group} {
		if strings.TrimSpace(field) == "" || len(field) > 128 {
			http.Error(w, "请填写完整的本地归属", 400)
			return
		}
	}
	for kind, value := range map[string]string{environmentKind: input.Environment, businessKind: input.Business} {
		key, err := catalogStableKey(ctx, o.store, kind, value)
		if err != nil {
			http.Error(w, "资源目录暂不可用", 503)
			return
		}
		if key == "" {
			http.Error(w, "请选择已登记的环境和业务线", 400)
			return
		}
	}
	priorEnvironment := current.Environment
	current.Name, current.Environment, current.Business, current.Group = input.Name, input.Environment, input.Business, input.Group
	raw, _ := json.Marshal(current)
	result, err := o.store.ExecContext(ctx, `UPDATE managed_clusters SET document=? WHERE id='local' AND
		(? OR NOT EXISTS(SELECT 1 FROM cluster_collection WHERE id='local' AND first_at>0))`, string(raw), priorEnvironment == "" || priorEnvironment == current.Environment)
	if err != nil {
		http.Error(w, "保存归属失败", 503)
		return
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		http.Error(w, "首次采集已成功，集群环境已锁定", 409)
		return
	}
	o.publishLocal(current)
	adminhttp.WriteJSON(w, map[string]string{"token": ""})
}

func (o *onboarding) maintain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, action := r.URL.Query().Get("id"), r.URL.Query().Get("action")
	if !clusterIDPattern.MatchString(id) {
		http.Error(w, "无效的集群 ID", 400)
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	spec, err := o.clusterSpec(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "实例不存在", 404)
		return
	}
	if err != nil || (id == "local" && o.local == nil) {
		http.Error(w, "集群目录暂不可用", 503)
		return
	}
	if action == "rotate-token" && r.Method == http.MethodPost {
		if spec.Mode != "push" || spec.Deleted {
			http.Error(w, "仅上报实例支持轮换上报凭据", 409)
			return
		}
		token := rand.Text() + rand.Text()
		if _, err := o.store.ExecContext(ctx, "UPDATE managed_clusters SET token_hash=? WHERE id=?", reportTokenHash(token), id); err != nil {
			http.Error(w, "上报凭据轮换失败", 503)
			return
		}
		adminhttp.WriteJSON(w, map[string]string{"token": token})
		return
	}
	reconnecting := action == "reconnect" && r.Method == http.MethodPost
	if (reconnecting && (id != "local" || !spec.Deleted)) || (!reconnecting && spec.Deleted) {
		http.Error(w, "当前状态不支持该操作", 409)
		return
	}
	tx, err := o.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "集群操作失败", 503)
		return
	}
	defer tx.Rollback()
	if id == "local" {
		spec.Enabled, spec.Deleted, spec.Generation = reconnecting, !reconnecting, rand.Text()
		data, _ := json.Marshal(spec)
		_, err = tx.ExecContext(ctx, "UPDATE managed_clusters SET document=? WHERE id=?", string(data), id)
	} else {
		_, err = tx.ExecContext(ctx, "INSERT INTO retired_clusters(id) VALUES(?)", id)
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM managed_clusters WHERE id=?", id)
		}
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM cluster_collection WHERE id=?", id)
	}
	if err == nil && !reconnecting {
		_, err = tx.ExecContext(ctx, "DELETE FROM namespace_business WHERE cluster=?", id)
	}
	if err != nil || tx.Commit() != nil {
		http.Error(w, "集群操作失败", 503)
		return
	}
	if id == "local" {
		o.publishLocal(spec)
	} else {
		o.registry.SetManaged(id, httpapi.ClusterTarget{})
	}
	w.WriteHeader(204)
}
