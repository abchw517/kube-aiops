package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/internal/config"
	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
	"github.com/abchw517/kube-aiops/internal/httpapi"
	"github.com/abchw517/kube-aiops/internal/identitystore"
	"github.com/abchw517/kube-aiops/internal/kubernetes"
	"github.com/abchw517/kube-aiops/internal/observability"
	"github.com/abchw517/kube-aiops/internal/sanitizer"
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
}

func newOnboarding(store *identitystore.Store, registry *httpapi.ClusterBackends) (*onboarding, error) {
	o := &onboarding{store: store, registry: registry}
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
	if spec.Mode == "pull" {
		if !remoteHTTPS(spec.APIURL) || !clusterIDPattern.MatchString(spec.EngineNamespace) || !clusterIDPattern.MatchString(spec.EngineName) {
			return httpapi.ClusterTarget{}, errors.New("需要 HTTPS Kubernetes 地址、Engine 命名空间和实例名")
		}
		dir := filepath.Join("/etc/kube-aiops/clusters", spec.ID)
		remote := kubernetes.NewClient(kubernetes.Config{ClusterID: spec.ID, ClusterName: spec.Name, Environment: spec.Environment,
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
	review, err := newFindingReview(backend, o.store, engineAI)
	if err != nil {
		return httpapi.ClusterTarget{}, err
	}
	review.correlator = evidence
	backend = review
	cluster.ReviewEnabled = engineAI != nil

	return httpapi.ClusterTarget{Backend: directoryBackend{Backend: backend, cluster: cluster, reports: reports}, Correlator: evidence}, nil
}

type directoryBackend struct {
	httpapi.Backend
	cluster kubernetes.Cluster
	reports *reportBackend
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
		rows, err := o.store.QueryContext(ctx, "SELECT document, reported_at FROM managed_clusters ORDER BY id")
		if err != nil {
			http.Error(w, "集群目录暂不可用", 503)
			return
		}
		defer rows.Close()
		type status struct {
			managedCluster
			ReportedAt int64 `json:"reportedAt"`
		}
		entries := make([]status, 0)
		for rows.Next() {
			var raw []byte
			var entry status
			if rows.Scan(&raw, &entry.ReportedAt) != nil || json.Unmarshal(raw, &entry.managedCluster) != nil {
				http.Error(w, "集群目录暂不可用", 503)
				return
			}
			entries = append(entries, entry)
		}
		if rows.Err() != nil {
			http.Error(w, "集群目录暂不可用", 503)
			return
		}
		writeAdminJSON(w, entries)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.WriteHeader(405)
		return
	}
	var spec managedCluster
	if !decodeAdmin(w, r, &spec) {
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
		if err := target.Backend.Ready(ctx); err != nil {
			http.Error(w, "连接或 Result 读取失败，请检查 TLS、只读凭据与 Engine", 502)
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
		if _, err := o.store.ExecContext(ctx, "UPDATE managed_clusters SET document=? WHERE id=?", string(raw), spec.ID); err != nil {
			http.Error(w, "保存失败", 503)
			return
		}
	}
	if !spec.Enabled {
		target = httpapi.ClusterTarget{}
	}
	o.registry.SetManaged(spec.ID, target)
	writeAdminJSON(w, map[string]string{"token": token})
}

func (o *onboarding) review(id string) *findingReview {
	target, _ := o.registry.Lookup(id)
	directory, _ := target.Backend.(directoryBackend)
	review, _ := directory.Backend.(*findingReview)
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
	if !decodeAdmin(w, r, &items) || items == nil || len(items) > 5000 {
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
