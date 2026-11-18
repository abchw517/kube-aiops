package portal

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

type collectionFixture struct {
	nodes  []kubernetes.NodeStatus
	result []finding.Finding
	err    error
}

func (f collectionFixture) ListNodeStatuses(context.Context) ([]kubernetes.NodeStatus, error) {
	return f.nodes, f.err
}
func (f collectionFixture) ListNamespaces(context.Context) ([]kubernetes.Namespace, error) {
	return []kubernetes.Namespace{{Name: "platform"}}, nil
}
func (f collectionFixture) ListFindings(context.Context, finding.Query) (finding.Page, error) {
	return finding.Page{Items: f.result}, nil
}

func TestPullCollectionLocksEnvironmentAndKeepsLastValidNodeSample(t *testing.T) {
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "clusters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	o, err := newOnboarding(store, &httpapi.ClusterBackends{Backend: testBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(method, env string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		input := `{"id":"remote","name":"remote","environment":"` + env + `","business":"ops","group":"apps","mode":"pull","enabled":true,"apiURL":"https://example.com","engineNamespace":"engine","engineName":"engine"}`
		o.admin(w, httptest.NewRequest(method, "/", strings.NewReader(input)))
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, env, w.Code, w.Body.String())
		}
	}
	create("POST", "研发", 200)
	ctx := t.Context()
	document := func() string {
		t.Helper()
		var raw string
		if err := store.QueryRowContext(ctx, "SELECT document FROM managed_clusters WHERE id='remote'").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	prior := document()
	create("PUT", "测试", 200)
	current := document()
	start := time.Now().Truncate(time.Second)
	diagnosisOnly := collectionFixture{result: []finding.Finding{{Source: finding.SourceK8sGPT}}}
	sample := func(raw string, source collectionFixture, offset time.Duration) collectionState {
		t.Helper()
		if err := o.collect(ctx, "remote", raw, source, start.Add(offset), true); err != nil {
			t.Fatal(err)
		}
		state, err := o.readCollection(ctx, "remote")
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	if state := sample(prior, diagnosisOnly, 0); state.FirstAt != 0 {
		t.Fatal("a sample from the old configuration locked the corrected environment")
	}
	if state := sample(current, diagnosisOnly, time.Second); state.FirstAt == 0 || state.Data.NodeSampleAt != 0 {
		t.Fatal("a diagnosis alone must lock the environment without reporting node health")
	}
	create("PUT", "生产", 409)
	nodes := collectionFixture{nodes: []kubernetes.NodeStatus{{Name: "worker", Ready: "False"}}}
	state := sample(current, nodes, 5*time.Minute)
	if state.Data.NodeSampleAt == 0 || len(state.Data.Nodes) != 1 {
		t.Fatal("late nodes must populate node health")
	}
	failed := collectionFixture{err: errors.New("unavailable")}
	sample(current, failed, 10*time.Minute)
	state = sample(current, failed, 15*time.Minute)
	if state.Data.NodeFailures != 2 || state.Data.NodeSampleAt != start.Add(5*time.Minute).Unix() || len(state.Data.Nodes) != 1 {
		t.Fatalf("last valid sample not retained: %+v", state.Data)
	}
	state = sample(current, nodes, 20*time.Minute)
	if state.Data.NodeFailures != 0 || state.FirstAt != start.Add(time.Second).Unix() {
		t.Fatalf("recovery altered the first collection or retained failures: %+v", state)
	}
}
