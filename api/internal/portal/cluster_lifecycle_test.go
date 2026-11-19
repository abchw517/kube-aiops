package portal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/httpapi"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

func lifecycleRequest(t *testing.T, o *onboarding, method, path, data string, want int) string {
	t.Helper()
	w := httptest.NewRecorder()
	o.admin(w, httptest.NewRequest(method, path, strings.NewReader(data)))
	if w.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body)
	}
	return w.Body.String()
}

func TestClusterConnectionMaintenance(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "clusters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := &httpapi.ClusterBackends{Backend: testBackend{}}
	o, err := newOnboarding(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	client := kubernetes.NewClient(kubernetes.Config{Environment: "研发", ClusterName: "本地集群"})
	if err := o.initLocal(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO resource_catalog(kind,id,name) VALUES ('business-lines','cloud','云帆')"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO resource_catalog_ids VALUES ('business-lines','cloud','cloud','cloud')"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO resource_catalog_names VALUES ('business-lines','云帆','cloud')"); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"local","mode":"local","name":"本地集群","environment":"研发","business":"云帆","group":"应用集群"}`
	lifecycleRequest(t, o, "PUT", "/", body, 200)
	if _, err := store.ExecContext(ctx, "INSERT INTO cluster_collection(id,first_at) VALUES ('local',100)"); err != nil {
		t.Fatal(err)
	}
	lifecycleRequest(t, o, "PUT", "/", strings.Replace(body, "研发", "测试", 1), 409)
	lifecycleRequest(t, o, "DELETE", "/?id=local", "", 204)
	if _, ok := registry.Local(); ok {
		t.Fatal("deleted local still exposed")
	}
	restarted, err := newOnboarding(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.initLocal(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Local(); ok {
		t.Fatal("restart exposed deleted local")
	}
	lifecycleRequest(t, restarted, "POST", "/?id=local&action=reconnect", "", 204)
	target, ok := registry.Local()
	if !ok || target.Backend.Clusters()[0].Business != "云帆" || target.Backend.Clusters()[0].Group != "应用集群" {
		t.Fatal("original local attribution not recovered")
	}
	collection, err := restarted.readCollection(ctx, "local")
	if err != nil || collection.FirstAt != 0 {
		t.Fatalf("old first collection retained: %+v %v", collection, err)
	}
	push := `{"id":"remote","mode":"push","name":"远程","environment":"测试","business":"云帆","group":"应用集群","enabled":true}`
	first := lifecycleRequest(t, o, "POST", "/", push, 200)
	second := lifecycleRequest(t, o, "POST", "/?id=remote&action=rotate-token", "", 200)
	if first == second {
		t.Fatal("rotation returned the previous token")
	}
	for _, fixture := range []struct {
		body string
		want int
	}{{first, 401}, {second, 400}} {
		var payload map[string]string
		if err := json.Unmarshal([]byte(fixture.body), &payload); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/?cluster=remote", strings.NewReader("[]"))
		request.Header.Set("Authorization", "Bearer "+payload["token"])
		o.report(w, request)
		if w.Code != fixture.want {
			t.Fatalf("report status: %d want %d", w.Code, fixture.want)
		}
	}
	lifecycleRequest(t, o, "DELETE", "/?id=remote", "", 204)
	if _, ok := registry.Lookup("remote"); ok {
		t.Fatal("deleted remote still exposed")
	}
	lifecycleRequest(t, o, "POST", "/", push, 409)
}

// TestExternalCredentialsAreRedactedAndRollbackOnFailure 覆盖凭据测试、加密存储和失败轮换。
func TestExternalCredentialsAreRedactedAndRollbackOnFailure(t *testing.T) {
	t.Setenv("CLUSTER_CREDENTIAL_KEY_FILE", "")
	t.Setenv("AUTH_LDAP_ENCRYPTION_KEY_FILE", "")
	api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "Bearer fixture-no-node-access" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-valid-cluster" && len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[]}`)
	}))
	api.TLS = &tls.Config{ClientAuth: tls.RequestClientCert, MinVersion: tls.VersionTLS12}
	api.StartTLS()
	defer api.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}))
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "clusters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	o, err := newOnboarding(store, &httpapi.ClusterBackends{Backend: testBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	spec := managedCluster{ID: "remote", Name: "远程", Mode: "pull", Enabled: true, Environment: "测试", Business: "云帆", Group: "应用",
		APIURL: api.URL, EngineNamespace: "engine", EngineName: "engine"}
	body := func(spec managedCluster, credential *credentialInput) string {
		t.Helper()
		raw, err := json.Marshal(struct {
			managedCluster
			Credential *credentialInput `json:"credential,omitempty"`
		}{spec, credential})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: active
clusters:
- name: test
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: active
  context: { cluster: test, user: readonly }
users:
- name: readonly
  user: { token: fixture-valid-cluster }
`, api.URL, base64.StdEncoding.EncodeToString([]byte(ca)))
	kubeconfigDraft := &credentialInput{Kind: "kubeconfig", Kubeconfig: kubeconfig}
	withKubeconfig := spec
	withKubeconfig.APIURL = ""

	key, err := x509.MarshalPKCS8PrivateKey(api.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &credentialInput{Kind: "certificate", CA: ca, Certificate: ca, PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))}
	token := &credentialInput{Kind: "token", Token: "fixture-valid-cluster", CA: ca}
	for _, fixture := range []struct {
		credential *credentialInput
		spec       managedCluster
	}{{kubeconfigDraft, withKubeconfig}, {token, spec}, {certificate, spec}} {
		t.Run("test without key/"+fixture.credential.Kind, func(t *testing.T) {
			lifecycleRequest(t, o, "POST", "/?test=true", body(fixture.spec, fixture.credential), http.StatusNoContent)
		})
	}
	denied := *kubeconfigDraft
	denied.Kubeconfig = strings.Replace(kubeconfig, "fixture-valid-cluster", "fixture-no-node-access", 1)
	if message := lifecycleRequest(t, o, "POST", "/?test=true", body(withKubeconfig, &denied), http.StatusBadGateway); !strings.Contains(message, "节点读取权限不足") {
		t.Fatalf("expected remote node permission failure, got %q", message)
	}
	lifecycleRequest(t, o, "POST", "/", body(spec, nil), http.StatusBadRequest)
	if _, err := os.Stat(store.Path() + ".cluster-credentials.key"); !os.IsNotExist(err) {
		t.Fatalf("connection test created a storage key: %v", err)
	}
	if list := lifecycleRequest(t, o, "GET", "/", "", http.StatusOK); strings.TrimSpace(list) != "[]" {
		t.Fatalf("test or rejected save persisted a cluster: %s", list)
	}
	if _, ok := o.registry.Lookup(spec.ID); ok {
		t.Fatal("test or rejected save registered a cluster")
	}

	// 从未配置外部密钥的全新环境首次保存即可成功。
	lifecycleRequest(t, o, "POST", "/", body(withKubeconfig, kubeconfigDraft), http.StatusOK)
	saved, err := o.clusterSpec(t.Context(), spec.ID)
	var config kubernetes.Config
	if err != nil || saved.APIURL != api.URL || saved.CredentialKind != "kubeconfig" || !strings.HasPrefix(saved.CredentialCipher, sqliteCredentialPrefix) ||
		o.configureCredential(saved, &config) != nil || config.Token != token.Token || strings.Contains(saved.CredentialCipher, token.Token) {
		t.Fatal("kubeconfig was not saved as usable encrypted credentials")
	}
	list := lifecycleRequest(t, o, "GET", "/", "", http.StatusOK)
	if strings.Contains(list, "credentialCipher") || strings.Contains(list, config.Token) || !strings.Contains(list, `"credentialKind":"kubeconfig"`) {
		t.Fatal("cluster directory exposed credentials or omitted kind")
	}

	reopened, err := identitystore.Open(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	o, err = newOnboarding(reopened, &httpapi.ClusterBackends{Backend: testBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleRequest(t, o, "PUT", "/?test=true", body(spec, nil), http.StatusNoContent)
	invalidToken := *token
	invalidToken.Token = "fixture-invalid-cluster"
	lifecycleRequest(t, o, "PUT", "/", body(spec, &invalidToken), http.StatusBadGateway)
	preserved, err := o.clusterSpec(t.Context(), spec.ID)
	target, ok := o.registry.Lookup(spec.ID)
	if err != nil || preserved != saved || !ok || target.Backend.(directoryBackend).spec != saved {
		t.Fatal("a test or failed rotation changed the stored or active connection")
	}
	lifecycleRequest(t, o, "PUT", "/", body(spec, token), http.StatusOK)
	lifecycleRequest(t, o, "PUT", "/", body(spec, certificate), http.StatusOK)

	legacy := spec
	legacy.ID = "legacy-mount"
	if _, err := store.ExecContext(t.Context(), "INSERT INTO managed_clusters(id,document,token_hash) VALUES (?,?,'')", legacy.ID, body(legacy, nil)); err != nil {
		t.Fatal(err)
	}
	o, err = newOnboarding(reopened, &httpapi.ClusterBackends{Backend: testBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := o.registry.Lookup(legacy.ID); ok {
		t.Fatal("legacy mount-only cluster should wait for credential entry")
	}
	lifecycleRequest(t, o, "PUT", "/", body(legacy, token), http.StatusOK)
	lifecycleRequest(t, o, "PUT", "/?test=true", body(legacy, nil), http.StatusNoContent)

	keyFile := store.Path() + ".cluster-credentials.key"
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	lifecycleRequest(t, o, "PUT", "/", body(legacy, token), http.StatusServiceUnavailable)
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatalf("missing key was silently replaced: %v", err)
	}

	if _, _, err := decodeKubeconfig(strings.Replace(kubeconfig, "user: { token: fixture-valid-cluster }", "user: { exec: { command: unsupported } }", 1)); err == nil {
		t.Fatal("executable kubeconfig credential accepted")
	}
}

type assignmentBackend struct{ testBackend }

func (assignmentBackend) ListNamespaces(context.Context) ([]kubernetes.Namespace, error) {
	return []kubernetes.Namespace{{Name: "payments"}, {Name: "platform"}}, nil
}

func TestNamespaceAssignmentsCommitTogether(t *testing.T) {
	ctx := t.Context()
	store, err := identitystore.Open(filepath.Join(t.TempDir(), "clusters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := &httpapi.ClusterBackends{Backend: assignmentBackend{}}
	o, err := newOnboarding(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	registry.SetLocal(httpapi.ClusterTarget{Backend: assignmentBackend{}})
	if _, err := store.ExecContext(ctx, `INSERT INTO resource_catalog_ids VALUES ('business-lines','cloud','cloud','cloud')`); err != nil {
		t.Fatal(err)
	}

	save := func(body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		o.namespaceAssignments(w, httptest.NewRequest("PUT", "/?cluster=local", strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
	}
	save(`{"namespace":"payments","businesses":["cloud"]}`, 204)
	save(`{"assignments":[{"namespace":"payments","businesses":[]},{"namespace":"platform","businesses":["missing"]}]}`, 409)
	assigned, err := o.namespaceBusiness(ctx, "local", "payments", "cloud")
	if err != nil || !assigned {
		t.Fatalf("partial update after invalid assignment: %v %v", assigned, err)
	}
	save(`{"assignments":[{"namespace":"payments","businesses":[]},{"namespace":"platform","businesses":["cloud"]}]}`, 204)
	payments, err := o.namespaceBusiness(ctx, "local", "payments", "cloud")
	if err != nil || payments {
		t.Fatalf("payments assignment remained: %v %v", payments, err)
	}
	platform, err := o.namespaceBusiness(ctx, "local", "platform", "cloud")
	if err != nil || !platform {
		t.Fatalf("platform assignment missing: %v %v", platform, err)
	}
	if _, err := store.ExecContext(ctx, `INSERT INTO resource_catalog_ids VALUES ('business-lines','ops','ops','ops');
		INSERT INTO namespace_business VALUES ('local','payments','ops'),('local','stale','cloud'),('other','platform','cloud')`); err != nil {
		t.Fatal(err)
	}
	save(`{"businessId":"cloud","add":["payments"],"remove":["platform"]}`, 204)
	save(`{"businessId":"cloud","add":["payments"],"remove":["platform"]}`, 204)
	save(`{"businessId":"cloud","add":["platform"],"remove":["missing"]}`, 404)
	save(`{"businessId":"cloud","add":["platform"],"remove":["platform"]}`, 400)
	save(`{"businessId":"cloud","add":["platform"],"assignments":[{"namespace":"payments","businesses":[]}]}`, 400)
	save(`{"businessId":"missing","add":["platform"]}`, 409)
	for _, relation := range []struct {
		cluster, namespace, business string
		want                         bool
	}{
		{"local", "payments", "cloud", true},
		{"local", "payments", "ops", true},
		{"local", "platform", "cloud", false},
		{"local", "stale", "cloud", true},
		{"other", "platform", "cloud", true},
	} {
		got, err := o.namespaceBusiness(ctx, relation.cluster, relation.namespace, relation.business)
		if err != nil || got != relation.want {
			t.Fatalf("unexpected association %+v: %v, %v", relation, got, err)
		}
	}
}
