package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/config"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/observability"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

type managedCluster struct {
	AIEnabled       bool   `json:"aiEnabled,omitempty"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Environment     string `json:"environment"`
	Business        string `json:"business"`
	Group           string `json:"group"`
	Mode            string `json:"mode"`
	Enabled         bool   `json:"enabled"`
	APIURL          string `json:"apiURL,omitempty"`
	EngineNamespace string `json:"engineNamespace,omitempty"`
	EngineName      string `json:"engineName,omitempty"`
	PrometheusURL   string `json:"prometheusURL,omitempty"`
	VictoriaLogsURL string `json:"victoriaLogsURL,omitempty"`
	AlertmanagerURL string `json:"alertmanagerURL,omitempty"`
}

type onboarding struct {
	store    *identitystore.Store
	registry *httpapi.ClusterBackends
	mu       sync.Mutex
	local    *kubernetes.Client
}

func newOnboarding(store *identitystore.Store, registry *httpapi.ClusterBackends) (*onboarding, error) {
	o := &onboarding{store: store, registry: registry}
	if err := initResourceCatalog(context.Background(), store); err != nil {
		return nil, err
	}
	if err := o.load(context.Background()); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *onboarding) load(ctx context.Context) error {
	rows, err := o.store.QueryContext(ctx, "SELECT document FROM managed_clusters")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var spec managedCluster
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &spec); err != nil {
			return err
		}
		target, err := o.build(spec)
		if err != nil {
			return err
		}
		if spec.Enabled {
			o.registry.SetManaged(spec.ID, target)
		}
	}
	return rows.Err()
}

func remoteHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func (o *onboarding) build(spec managedCluster) (httpapi.ClusterTarget, error) {
	if !clusterIDPattern.MatchString(spec.ID) || spec.ID == "local" {
		return httpapi.ClusterTarget{}, errors.New("实例 ID 无效或被保留")
	}
	if _, exists := o.registry.Targets[spec.ID]; exists {
		return httpapi.ClusterTarget{}, errors.New("实例 ID 已在文件注册表中使用")
	}
	if spec.Mode != "push" && spec.Mode != "pull" {
		return httpapi.ClusterTarget{}, errors.New("接入方式无效")
	}
	for _, label := range []string{spec.Name, spec.Environment, spec.Business, spec.Group} {
		if strings.TrimSpace(label) == "" || len(label) > 128 {
			return httpapi.ClusterTarget{}, errors.New("请填写实例名称及完整的环境、业务和集群分组")
		}
	}
	cluster := kubernetes.Cluster{ID: spec.ID, Name: spec.Name, Environment: spec.Environment, Business: spec.Business, Group: spec.Group, Status: "ready"}
	var backend httpapi.Backend
	var engineAI func(context.Context) (kubernetes.EngineAIConfig, error)
	var reports *reportBackend
	var remote *kubernetes.Client
	if spec.Mode == "pull" {
		if !remoteHTTPS(spec.APIURL) || !clusterIDPattern.MatchString(spec.EngineNamespace) || !clusterIDPattern.MatchString(spec.EngineName) {
			return httpapi.ClusterTarget{}, errors.New("需要 HTTPS Kubernetes 地址、Engine 命名空间和实例名")
		}
		dir := filepath.Join("/etc/kube-aiops/clusters", spec.ID)
		remote = kubernetes.NewClient(kubernetes.Config{ClusterID: spec.ID, ClusterName: spec.Name, Environment: spec.Environment,
			APIURL: spec.APIURL, TokenFile: filepath.Join(dir, "token"), CAFile: filepath.Join(dir, "ca.crt"), K8sGPTNS: spec.EngineNamespace, K8sGPTName: spec.EngineName})
		backend = remote
		if spec.AIEnabled {
			approvedEndpoint := strings.TrimSpace(os.Getenv("FINDING_AI_ALLOWED_ENDPOINT_" + strings.ToUpper(strings.ReplaceAll(spec.ID, "-", "_"))))
			if approvedEndpoint == "" {
				approvedEndpoint = strings.TrimSpace(os.Getenv("FINDING_AI_ALLOWED_ENDPOINT"))
			}
			engineAI = func(ctx context.Context) (kubernetes.EngineAIConfig, error) {
				return remote.EngineAI(ctx, approvedEndpoint)
			}
		}
	} else {
		reports = &reportBackend{store: o.store, id: spec.ID}
		backend = reports
	}
	for _, endpoint := range []string{spec.PrometheusURL, spec.VictoriaLogsURL, spec.AlertmanagerURL} {
		if endpoint != "" && !remoteHTTPS(endpoint) {
			return httpapi.ClusterTarget{}, errors.New("观测源需要 HTTPS 地址")
		}
	}
	source := func(endpoint string) observability.Config {
		if endpoint == "" {
			return observability.Config{}
		}
		return observability.Config{URL: endpoint, ClusterLabelValue: spec.ID}
	}
	events, _ := backend.(correlation.EventSource)
	evidence, err := buildCorrelator(config.Config{Prometheus: source(spec.PrometheusURL), VictoriaLogs: source(spec.VictoriaLogsURL), Alertmanager: source(spec.AlertmanagerURL)}, events)
	if err != nil {
		return httpapi.ClusterTarget{}, err
	}
	if spec.AIEnabled && spec.Mode != "pull" {
		return httpapi.ClusterTarget{}, errors.New("读取 Engine Secret 需要手动接入的 Kubernetes API 连接")
	}
	review, err := diagnosis.New(backend, o.store, engineAI)
	if err != nil {
		return httpapi.ClusterTarget{}, err
	}
	review.SetCorrelator(evidence)
	backend = review
	cluster.ReviewEnabled = engineAI != nil

	return httpapi.ClusterTarget{Backend: directoryBackend{Backend: backend, cluster: cluster, reports: reports, remote: remote, spec: spec}, Correlator: evidence}, nil
}

type directoryBackend struct {
	httpapi.Backend
	cluster kubernetes.Cluster
	reports *reportBackend
	remote  *kubernetes.Client
	spec    managedCluster
}

func (b directoryBackend) Clusters() []kubernetes.Cluster {
	instance := b.cluster
	if b.reports != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		instance.Status = "waiting"
		if err := b.reports.store.QueryRowContext(ctx, "SELECT reported_at FROM managed_clusters WHERE id=?", instance.ID).Scan(&instance.LastReportAt); err != nil {
			instance.Status = "unknown"
		} else if instance.LastReportAt > 0 {
			instance.Status = "online"
			if time.Since(time.Unix(instance.LastReportAt, 0)) > 3*time.Minute {
				instance.Status = "offline"
			}
		}
	}
	return []kubernetes.Cluster{instance}
}

func (o *onboarding) admin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		rows, err := o.store.QueryContext(ctx, "SELECT m.document, m.reported_at, COALESCE(c.first_at,0), COALESCE(c.validated_at,0), COALESCE(c.checked_at,0), COALESCE(c.document,'{}') FROM managed_clusters m LEFT JOIN cluster_collection c ON c.id=m.id ORDER BY m.id")
		if err != nil {
			http.Error(w, "集群目录暂不可用", 503)
			return
		}
		defer rows.Close()
		type status struct {
			managedCluster
			ReportedAt int64 `json:"reportedAt"`
			collectionState
		}
		entries := make([]status, 0)
		for rows.Next() {
			var raw, collection []byte
			var entry status
			if rows.Scan(&raw, &entry.ReportedAt, &entry.FirstAt, &entry.ValidatedAt, &entry.CheckedAt, &collection) != nil || json.Unmarshal(raw, &entry.managedCluster) != nil || json.Unmarshal(collection, &entry.Data) != nil {
				http.Error(w, "集群目录暂不可用", 503)
				return
			}
			entries = append(entries, entry)
		}
		if rows.Err() != nil {
			http.Error(w, "集群目录暂不可用", 503)
			return
		}
		rows.Close()
		if o.local != nil {
			var local status
			cluster := o.local.Clusters()[0]
			local.managedCluster = managedCluster{ID: cluster.ID, Name: cluster.Name, Environment: cluster.Environment, Mode: "local", Enabled: true}
			local.collectionState, err = o.readCollection(ctx, cluster.ID)
			if err != nil {
				http.Error(w, "集群目录暂不可用", 503)
				return
			}
			entries = append([]status{local}, entries...)
		}
		adminhttp.WriteJSON(w, entries)
		return
	}
	if r.Method == http.MethodPost && r.URL.Query().Get("action") == "recheck" {
		id := r.URL.Query().Get("id")
		var raw string
		err := o.store.QueryRowContext(ctx, "SELECT document FROM managed_clusters WHERE id=?", id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "实例不存在", 404)
			return
		}
		target, _ := o.registry.Lookup(id)
		directory, _ := target.Backend.(directoryBackend)
		if err != nil || directory.remote == nil {
			http.Error(w, "仅已启用的手动接入实例支持重新校验", 409)
			return
		}
		var spec managedCluster
		if json.Unmarshal([]byte(raw), &spec) != nil || directory.spec != spec {
			http.Error(w, "集群配置已更新，请重试", 409)
			return
		}
		if err := o.collect(ctx, id, raw, directory.remote, time.Now(), true); err != nil {
			http.Error(w, "重新校验失败", 503)
			return
		}
		w.WriteHeader(204)
		return
	}

	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.WriteHeader(405)
		return
	}
	var spec managedCluster
	if !adminhttp.Decode(w, r, &spec) {
		http.Error(w, "集群配置无效", 400)
		return
	}
	target, err := o.build(spec)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if r.URL.Query().Get("test") == "true" {
		if spec.Mode != "pull" {
			http.Error(w, "仅手动接入需要测试连接", 400)
			return
		}
		if _, err := target.Backend.(directoryBackend).remote.ListNodeStatuses(ctx); err != nil {
			http.Error(w, nodeCollectionError(err), 502)
			return
		}
		w.WriteHeader(204)
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	raw, _ := json.Marshal(spec)
	var token string
	if r.Method == http.MethodPost {
		hash := ""
		if spec.Mode == "push" {
			token = rand.Text() + rand.Text()
			hash = reportTokenHash(token)
		}
		result, err := o.store.ExecContext(ctx, "INSERT INTO managed_clusters(id,document,token_hash) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", spec.ID, string(raw), hash)
		if err != nil {
			http.Error(w, "保存失败", 503)
			return
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			http.Error(w, "实例 ID 已存在", 409)
			return
		}
	} else {
		var prior []byte
		var previous managedCluster
		if err := o.store.QueryRowContext(ctx, "SELECT document FROM managed_clusters WHERE id=?", spec.ID).Scan(&prior); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "实例不存在", 404)
			} else {
				http.Error(w, "目录暂不可用", 503)
			}
			return
		}
		if json.Unmarshal(prior, &previous) != nil {
			http.Error(w, "目录暂不可用", 503)
			return
		}
		if spec.Mode != previous.Mode {
			http.Error(w, "接入方式需要使用新的实例 ID", 409)
			return
		}
		tx, err := o.store.BeginTx(ctx, nil)
		if err != nil {
			http.Error(w, "保存失败", 503)
			return
		}
		defer tx.Rollback()
		result, err := tx.ExecContext(ctx, `UPDATE managed_clusters SET document=? WHERE id=? AND
			(? OR NOT EXISTS (SELECT 1 FROM cluster_collection WHERE id=? AND first_at > 0))`,
			string(raw), spec.ID, spec.Environment == previous.Environment, spec.ID)
		if err != nil {
			http.Error(w, "保存失败", 503)
			return
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			http.Error(w, "首次采集已成功，集群环境已锁定", 409)
			return
		}
		if spec.Mode == "pull" && (spec.APIURL != previous.APIURL || spec.EngineNamespace != previous.EngineNamespace ||
			spec.EngineName != previous.EngineName || spec.Enabled && !previous.Enabled) {
			if _, err := tx.ExecContext(ctx, "UPDATE cluster_collection SET checked_at=0,validated_at=0,document='{}' WHERE id=?", spec.ID); err != nil {
				http.Error(w, "保存失败", 503)
				return
			}
		}
		if err := tx.Commit(); err != nil {
			http.Error(w, "保存失败", 503)
			return
		}
	}
	if !spec.Enabled {
		target = httpapi.ClusterTarget{}
	}
	o.registry.SetManaged(spec.ID, target)
	adminhttp.WriteJSON(w, map[string]string{"token": token})
}

func (o *onboarding) review(id string) *diagnosis.Review {
	target, _ := o.registry.Lookup(id)
	directory, _ := target.Backend.(directoryBackend)
	review, _ := directory.Backend.(*diagnosis.Review)
	return review
}

func reportTokenHash(token string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(token))) }

func (o *onboarding) reporting(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/collector/snapshot", o.report)
	mux.Handle("/", next)
	return mux
}

func (o *onboarding) report(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.URL.Query().Get("cluster")
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(bearer) > 256 || !clusterIDPattern.MatchString(id) {
		w.WriteHeader(401)
		return
	}
	ctx := r.Context()
	var hash string
	var raw []byte
	var spec managedCluster
	err := o.store.QueryRowContext(ctx, "SELECT token_hash,document FROM managed_clusters WHERE id=?", id).Scan(&hash, &raw)
	if err != nil || hash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(reportTokenHash(bearer))) != 1 {
		w.WriteHeader(401)
		return
	}
	if json.Unmarshal(raw, &spec) != nil || spec.Mode != "push" || !spec.Enabled {
		w.WriteHeader(403)
		return
	}
	captured, err := time.Parse(time.RFC3339Nano, r.Header.Get("X-Snapshot-Captured-At"))
	now := time.Now()
	if err != nil || captured.After(now.Add(time.Minute)) || captured.Before(now.Add(-15*time.Minute)) {
		http.Error(w, "快照时间无效", 400)
		return
	}
	var items []finding.Finding
	if !adminhttp.Decode(w, r, &items) || items == nil || len(items) > 5000 {
		http.Error(w, "无效快照", 400)
		return
	}
	seen := make(map[string]bool)
	uids := map[string]bool{}
	for i, item := range items {
		item.Cluster = id
		safe, err := sanitizer.Default().Finding(item)
		if err != nil || seen[item.ID] || (item.ResultUID != "" && uids[item.ResultUID]) {
			http.Error(w, "无效或重复诊断", 400)
			return
		}
		seen[item.ID] = true
		uids[item.ResultUID] = true
		items[i] = safe
	}
	payload, _ := json.Marshal(items)
	result, err := o.store.ExecContext(ctx, "UPDATE managed_clusters SET snapshot=?, reported_at=?, captured_at=? WHERE id=? AND document=? AND captured_at < ?",
		string(payload), now.Unix(), captured.UnixNano(), id, string(raw), captured.UnixNano())
	if err != nil {
		w.WriteHeader(503)
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		var last int64
		if readErr := o.store.QueryRowContext(ctx, "SELECT captured_at FROM managed_clusters WHERE id=? AND document=?", id, string(raw)).Scan(&last); readErr != nil || last != captured.UnixNano() {
			w.WriteHeader(409)
			return
		}
	}
	w.WriteHeader(204)
}

type reportBackend struct {
	store *identitystore.Store
	id    string
}

func (b *reportBackend) Ready(context.Context) error    { return nil }
func (b *reportBackend) Clusters() []kubernetes.Cluster { return nil }
func (b *reportBackend) snapshot(ctx context.Context) ([]finding.Finding, error) {
	var raw []byte
	var items []finding.Finding
	err := b.store.QueryRowContext(ctx, "SELECT snapshot FROM managed_clusters WHERE id=?", b.id).Scan(&raw)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &items)
	return items, err
}
func (b *reportBackend) ListNamespaces(ctx context.Context) ([]kubernetes.Namespace, error) {
	items, err := b.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	namespaces := make([]kubernetes.Namespace, 0)
	for _, item := range items {
		if item.Namespace != "" && !seen[item.Namespace] {
			seen[item.Namespace] = true
			namespaces = append(namespaces, kubernetes.Namespace{Name: item.Namespace})
		}
	}
	sort.Slice(namespaces, func(i, j int) bool { return namespaces[i].Name < namespaces[j].Name })
	return namespaces, nil
}
func (b *reportBackend) GetResource(context.Context, string, string, string) (kubernetes.ResourceDetail, error) {
	return kubernetes.ResourceDetail{}, &kubernetes.APIError{StatusCode: 404}
}
func (b *reportBackend) GetFinding(ctx context.Context, id string) (finding.Finding, error) {
	items, err := b.snapshot(ctx)
	if err != nil {
		return finding.Finding{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return finding.Finding{}, &kubernetes.APIError{StatusCode: 404}
}
func (b *reportBackend) filtered(ctx context.Context, filter finding.Filter) ([]finding.Finding, error) {
	items, err := b.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return kubernetes.FilterFindings(items, filter)
}
func (b *reportBackend) ListFindings(ctx context.Context, q finding.Query) (finding.Page, error) {
	items, err := b.filtered(ctx, q.Filter)
	if err != nil {
		return finding.Page{}, err
	}
	return kubernetes.PageFindings(items, q)
}
func (b *reportBackend) SummarizeFindings(ctx context.Context, f finding.Filter) (finding.Summary, error) {
	items, err := b.filtered(ctx, f)
	if err != nil {
		return finding.Summary{}, err
	}
	return kubernetes.SummarizeFindings(items), nil
}
