package portal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

const nodeCollectionInterval = 5 * time.Minute

type collectionState struct {
	FirstAt     int64          `json:"firstCollectedAt"`
	ValidatedAt int64          `json:"validatedAt"`
	CheckedAt   int64          `json:"checkedAt"`
	Data        collectionData `json:"collection"`
}

type collectionData struct {
	Nodes           []kubernetes.NodeStatus `json:"nodes,omitempty"`
	Namespaces      []kubernetes.Namespace  `json:"namespaces,omitempty"`
	NodeSampleAt    int64                   `json:"nodeSampleAt,omitempty"`
	NodeFailures    int                     `json:"nodeFailures,omitempty"`
	NodeError       string                  `json:"nodeError,omitempty"`
	NamespaceError  bool                    `json:"namespaceError,omitempty"`
	FindingError    bool                    `json:"findingError,omitempty"`
	FindingCount    int                     `json:"findingCount,omitempty"`
	WorkloadFinding bool                    `json:"workloadFinding,omitempty"`
}

type collectionSource interface {
	ListNodeStatuses(context.Context) ([]kubernetes.NodeStatus, error)
	ListNamespaces(context.Context) ([]kubernetes.Namespace, error)
	ListFindings(context.Context, finding.Query) (finding.Page, error)
}

func nodeCollectionError(err error) string {
	var apiErr *kubernetes.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return "节点读取权限不足"
		case http.StatusUnauthorized:
			return "Kubernetes 凭据无效"
		}
	}
	return "节点读取失败，请检查网络、TLS 和只读凭据"
}

func (o *onboarding) readCollection(ctx context.Context, id string) (collectionState, error) {
	var state collectionState
	var data []byte
	err := o.store.QueryRowContext(ctx, "SELECT first_at,validated_at,checked_at,document FROM cluster_collection WHERE id=?", id).
		Scan(&state.FirstAt, &state.ValidatedAt, &state.CheckedAt, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &state.Data)
	}
	return state, err
}

func (o *onboarding) collect(ctx context.Context, id, configuration string, source collectionSource, now time.Time, force bool) error {
	state, err := o.readCollection(ctx, id)
	if err != nil {
		return err
	}
	lastCheck := time.Unix(state.CheckedAt, 0)
	interval := nodeCollectionInterval
	if state.ValidatedAt > 0 && state.FirstAt == 0 && now.Before(time.Unix(state.ValidatedAt, 0).Add(nodeCollectionInterval)) {
		interval = 30 * time.Second
	}
	if !force && (now.Sub(lastCheck) < interval ||
		(configuration != "" && state.CheckedAt > 0 && state.ValidatedAt == 0 && state.FirstAt == 0)) {
		return nil
	}

	var nodes []kubernetes.NodeStatus
	var namespaces []kubernetes.Namespace
	var summary finding.Page
	var nodesErr, namespacesErr, findingsErr error
	var readings sync.WaitGroup
	readings.Go(func() {
		request, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		nodes, nodesErr = source.ListNodeStatuses(request)
	})
	readings.Go(func() {
		request, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		namespaces, namespacesErr = source.ListNamespaces(request)
	})
	readings.Go(func() {
		request, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		summary, findingsErr = source.ListFindings(request, finding.Query{Limit: 100})
	})
	readings.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}

	data := state.Data
	if nodesErr == nil && len(nodes) > 0 {
		data.Nodes, data.NodeSampleAt = nodes, now.Unix()
		data.NodeFailures, data.NodeError = 0, ""
	} else if data.NodeSampleAt == 0 || now.Sub(lastCheck) >= nodeCollectionInterval {
		data.NodeFailures++
		data.NodeError = "暂未找到包含 Ready 状态的节点"
		if nodesErr != nil {
			data.NodeError = nodeCollectionError(nodesErr)
		}
	}
	if namespacesErr == nil {
		data.Namespaces = namespaces
	}
	data.NamespaceError = namespacesErr != nil
	data.FindingError = findingsErr != nil
	var found bool
	if findingsErr == nil {
		data.FindingCount = len(summary.Items)
		data.WorkloadFinding = false
		for _, item := range summary.Items {
			found = found || item.Source == finding.SourceK8sGPT
			data.WorkloadFinding = data.WorkloadFinding || strings.EqualFold(item.Resource.Kind, "Pod") || strings.EqualFold(item.Resource.Kind, "PersistentVolumeClaim")
		}
	}
	if state.ValidatedAt == 0 && (nodesErr == nil || found) {
		state.ValidatedAt = now.Unix()
	}
	if state.FirstAt == 0 && ((nodesErr == nil && len(nodes) > 0) || found) {
		state.FirstAt = now.Unix()
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	// The observation belongs to the configuration it queried. A concurrent edit
	// can win first, in which case this sample is discarded and polled again.
	_, err = o.store.ExecContext(ctx, `INSERT INTO cluster_collection(id,first_at,validated_at,checked_at,document)
		SELECT ?,?,?,?,? WHERE ? = '' OR EXISTS(SELECT 1 FROM managed_clusters WHERE id=? AND document=?)
		ON CONFLICT(id) DO UPDATE SET first_at=CASE WHEN cluster_collection.first_at=0 THEN excluded.first_at ELSE cluster_collection.first_at END,
		validated_at=excluded.validated_at, checked_at=excluded.checked_at, document=excluded.document
		WHERE cluster_collection.checked_at < excluded.checked_at`,
		id, state.FirstAt, state.ValidatedAt, now.Unix(), string(raw), configuration, id, configuration)
	return err
}

func (o *onboarding) pollCollections(ctx context.Context) error {
	if o.local != nil {
		if err := o.collect(ctx, o.local.Clusters()[0].ID, "", o.local, time.Now(), false); err != nil {
			return err
		}
	}
	rows, err := o.store.QueryContext(ctx, "SELECT document FROM managed_clusters WHERE json_extract(document,'$.mode')='pull' AND json_extract(document,'$.enabled')")
	if err != nil {
		return err
	}
	var configurations []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		configurations = append(configurations, raw)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, raw := range configurations {
		var spec managedCluster
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			return err
		}
		target, ok := o.registry.Lookup(spec.ID)
		directory, _ := target.Backend.(directoryBackend)
		// Skip this round if an admin is replacing the registry entry.
		if !ok || directory.remote == nil || directory.spec != spec {
			continue
		}
		if err := o.collect(ctx, spec.ID, raw, directory.remote, time.Now(), false); err != nil {
			return err
		}
	}
	return nil
}
