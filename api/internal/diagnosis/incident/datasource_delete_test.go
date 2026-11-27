package incident

import (
	"context"
	"net/http"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func TestZabbixRenameAndDeleteRespectScopeAndVersion(t *testing.T) {
	f := newFixture(t)
	f.server.Zabbix = &ZabbixIntegration{
		Authorize: func(_ context.Context, p identity.Principal, code string, scope ZabbixScope) (bool, error) {
			return scope.Business == "ops" && (p.Subject == "admin" || p.Subject == "writer" && code == ConnectorWrite), nil
		},
	}
	f.server.registerZabbix(f.mux)
	ctx := t.Context()
	d := zabbixSourceRecord{ZabbixSource: ZabbixSource{ID: "z", Name: "旧名称", Endpoint: "https://zabbix.example",
		Scope: ZabbixScope{"prod", "ops"}, Groups: []assetmeta.ZabbixGroup{{ID: "1"}}, Enabled: true}, credential: []byte("opaque")}
	if _, err := f.server.Store.saveZabbix(ctx, d, "admin", true, false); err != nil {
		t.Fatal(err)
	}
	job, err := f.server.Store.claimZabbix(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.Store.finishZabbix(ctx, job, assetmeta.ZabbixSnapshot{Hosts: []assetmeta.ZabbixHost{{ID: "1"}}}, nil); err != nil {
		t.Fatal(err)
	}
	path := "/zabbix/sources/z"
	renamed := decoded[ZabbixSource](t, f.request("PATCH", path, "writer", `{"name":"  新名称  "}`, "", `"1"`), 200)
	if renamed.Name != "新名称" || renamed.Version != 2 || renamed.HostCount == nil || *renamed.HostCount != 1 {
		t.Fatalf("rename reset the source or lost its snapshot: %+v", renamed)
	}
	stored, err := zabbixByID(ctx, f.db.DB, d.ID)
	if err != nil || string(stored.credential) != "opaque" {
		t.Fatalf("rename lost its credential: %v", err)
	}
	for _, tc := range []struct {
		account, version string
		want             int
	}{
		{"writer", `"2"`, http.StatusNotFound},
		{"admin", `"1"`, http.StatusPreconditionFailed},
		{"admin", `"2"`, http.StatusNoContent},
		{"admin", `"2"`, http.StatusNotFound},
	} {
		res := f.request("DELETE", path, tc.account, "", "", tc.version)
		if res.Code != tc.want {
			t.Fatalf("delete as %s with %s: %d %s, want %d", tc.account, tc.version, res.Code, res.Body.String(), tc.want)
		}
	}
}

func TestDeletingAssetDatasourcePreservesIncidentReferences(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for _, sql := range []string{
		`INSERT INTO diagnostic_datasource(id,scope,document,version,enabled) VALUES('source','{}','{"name":"test","config":{"kind":"aliyun-ecs"}}',1,1)`,
		`INSERT INTO diagnostic_asset VALUES('asset','source','{}',1)`,
		`INSERT INTO diagnostic_dependency VALUES('incident','asset','asset','monitored_by','confirmed','','admin',1,'')`,
	} {
		if _, err := f.db.DB.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	res := f.request("DELETE", "/datasources/source", "admin", "", "", `"1"`)
	if res.Code != http.StatusConflict {
		t.Fatalf("deleted a referenced datasource: %d %s", res.Code, res.Body.String())
	}
	if _, err := f.db.DB.ExecContext(ctx, "DELETE FROM diagnostic_dependency WHERE source_id='incident'"); err != nil {
		t.Fatal(err)
	}
	res = f.request("DELETE", "/datasources/source", "admin", "", "", `"1"`)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d %s", res.Code, res.Body.String())
	}
	var count int
	if err := f.db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM diagnostic_asset WHERE datasource_id='source'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("asset cleanup: count=%d, err=%v", count, err)
	}
}

func TestDeleteObservabilityRequiresMatchingScope(t *testing.T) {
	f := newFixture(t)
	f.server.Zabbix = &ZabbixIntegration{
		Authorize: func(_ context.Context, p identity.Principal, code string, scope ZabbixScope) (bool, error) {
			return p.Subject == "admin" && code == ConnectorDelete && scope == (ZabbixScope{"prod", "ops"}), nil
		},
		ClusterBinding: func(_ context.Context, _ ZabbixScope, cluster string) (bool, error) {
			return cluster == "local", nil
		},
	}
	f.server.Observability = &ObservabilityIntegration{}
	f.server.registerObservability(f.mux)
	_, err := f.db.DB.ExecContext(t.Context(), `INSERT INTO diagnostic_observability
		(id,kind,environment,business,cluster,spec,credential) VALUES
		('scope-test','alertmanager','prod','ops','local',
		'{"kind":"alertmanager","scope":{"environmentId":"prod","businessId":"ops","clusterId":"local"}}','opaque')`)
	if err != nil {
		t.Fatal(err)
	}
	path := "/observability/sources/scope-test"
	if res := f.request("DELETE", path, "readonly", "", "", `"1"`); res.Code != http.StatusForbidden {
		t.Fatalf("unauthorized delete: %d %s", res.Code, res.Body.String())
	}
	if res := f.request("DELETE", path, "admin", "", "", `"1"`); res.Code != http.StatusNoContent {
		t.Fatalf("authorized delete: %d %s", res.Code, res.Body.String())
	}
	if res := f.request("DELETE", path, "admin", "", "", `"1"`); res.Code != http.StatusNotFound {
		t.Fatalf("deleted record remained accessible: %d %s", res.Code, res.Body.String())
	}
}
