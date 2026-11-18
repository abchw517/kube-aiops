package portal

import (
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

func TestExternalCredentialsAreRedactedAndRollbackOnFailure(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(make([]byte, 32))), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUSTER_CREDENTIAL_KEY_FILE", keyPath)
	api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	body := func(token string) string {
		raw, err := json.Marshal(struct {
			managedCluster
			Credential credentialInput `json:"credential"`
		}{spec, credentialInput{Kind: "token", Token: token, CA: ca}})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	lifecycleRequest(t, o, "POST", "/?test=true", body("fixture-valid-cluster"), 204)
	lifecycleRequest(t, o, "POST", "/", body("fixture-valid-cluster"), 200)
	lifecycleRequest(t, o, "PUT", "/", body("fixture-invalid-cluster"), 502)
	old, err := o.clusterSpec(t.Context(), "remote")
	var config kubernetes.Config
	if err != nil || old.configureCredential(&config) != nil || config.Token != "fixture-valid-cluster" || strings.Contains(old.CredentialCipher, config.Token) {
		t.Fatal("rotation did not keep encrypted original credentials")
	}
	list := lifecycleRequest(t, o, "GET", "/", "", 200)
	if strings.Contains(list, "credentialCipher") || strings.Contains(list, config.Token) || !strings.Contains(list, `"credentialKind":"token"`) {
		t.Fatal("cluster directory exposed credentials or omitted kind")
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
	raw, _ := json.Marshal(struct {
		managedCluster
		Credential credentialInput `json:"credential"`
	}{spec, credentialInput{Kind: "kubeconfig", Kubeconfig: kubeconfig}})
	lifecycleRequest(t, o, "PUT", "/", string(raw), 200)
	key, err := x509.MarshalPKCS8PrivateKey(api.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificateBody, _ := json.Marshal(struct {
		managedCluster
		Credential credentialInput `json:"credential"`
	}{
		spec, credentialInput{Kind: "certificate", CA: ca, Certificate: ca, PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))}})
	lifecycleRequest(t, o, "PUT", "/", string(certificateBody), 200)

	if _, _, err := decodeKubeconfig(strings.Replace(kubeconfig, "user: { token: fixture-valid-cluster }", "user: { exec: { command: unsupported } }", 1)); err == nil {
		t.Fatal("executable kubeconfig credential accepted")
	}
}
