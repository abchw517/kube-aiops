package portal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
)

func TestZabbixCredentialSurvivesRestartAndIsBoundToSource(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "portal.db")
	store, err := identitystore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = incident.Open(t.Context(), store.DB, nil); err != nil {
		t.Fatal(err)
	}
	config := configureZabbix(store, nil, nil, nil, func(string) bool { return true })
	c := assetmeta.ZabbixCredential{Method: "password", Username: "fixture-user", Secret: "fixture-private-password"}
	endpoint := "https://zabbix.example/api_jsonrpc.php"
	content, err := config.Seal(t.Context(), "source", endpoint, c)
	if err != nil || bytes.Contains(content, []byte(c.Secret)) {
		t.Fatalf("invalid credential encryption: %v", err)
	}
	info, err := os.Stat(dbPath + ".zabbix-credentials.key")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("invalid storage key: %v", err)
	}
	restarted := configureZabbix(store, nil, nil, nil, func(string) bool { return true })
	value, err := restarted.Open("source", endpoint, content)
	if err != nil || value != c {
		t.Fatalf("cannot read saved credential after restart: %v", err)
	}
	for _, candidate := range [][2]string{{"other", endpoint}, {"source", "https://other.example/api_jsonrpc.php"}} {
		if _, err := restarted.Open(candidate[0], candidate[1], content); err == nil {
			t.Fatal("credential decrypted for another source or endpoint")
		}
	}
}
