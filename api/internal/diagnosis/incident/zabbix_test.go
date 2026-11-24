package incident

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type zabbixFixture struct {
	snapshot assetmeta.ZabbixSnapshot
	err      error
}

func (*zabbixFixture) ZabbixEndpoint(value string) (string, error) { return value, nil }
func (*zabbixFixture) TestZabbix(context.Context, string, assetmeta.ZabbixCredential) (string, []assetmeta.ZabbixGroup, error) {
	return "6.0.36", []assetmeta.ZabbixGroup{{ID: "10", Name: "Windows"}, {ID: "20", Name: "TLS"}}, nil
}
func (c *zabbixFixture) ZabbixProblems(context.Context, string, assetmeta.ZabbixCredential, []string, []string) (assetmeta.ZabbixSnapshot, error) {
	return c.snapshot, c.err
}

func TestZabbixOnboardingSnapshotAndAuthorization(t *testing.T) {
	f := newFixture(t)
	host := assetmeta.ZabbixHost{ID: "80", Name: "Windows", Tags: []assetmeta.ZabbixTag{{Tag: "kube-aiops.cluster-id", Value: "ack-prod"}}}
	row := assetmeta.ZabbixProblem{ID: "20", TriggerID: "17", Name: "Memory alarm", Clock: strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10),
		Severity: "4", Hosts: []assetmeta.ZabbixHost{host}}
	c := &zabbixFixture{snapshot: assetmeta.ZabbixSnapshot{Hosts: []assetmeta.ZabbixHost{host}, Problems: []assetmeta.ZabbixProblem{row}, HostsVerified: true}}
	readAuth := f.server.Authorize
	f.server.Authorize = func(ctx context.Context, p identity.Principal, code string, s Scope, collection bool) (bool, error) {
		if p.Subject == "readonly" && code == ConnectorRead {
			return true, nil
		}
		return readAuth(ctx, p, code, s, collection)
	}
	f.server.Zabbix = &ZabbixIntegration{
		Collector: c,
		Authorize: func(_ context.Context, p identity.Principal, code string, scope ZabbixScope) (bool, error) {
			return p.Subject == "admin" && scope.Business == "ops" || p.Subject == "readonly" && code == ConnectorRead && scope.Business == "different", nil
		},
		Catalog: func(context.Context) (ZabbixScopeOptions, error) {
			return ZabbixScopeOptions{Environments: []ZabbixOption{{"production", "生产"}}, Businesses: []ZabbixOption{{"ops", "运维"}}}, nil
		},
		Seal: func(context.Context, string, string, assetmeta.ZabbixCredential) ([]byte, error) {
			return []byte("test-ciphertext"), nil
		},
		Open: func(string, string, []byte) (assetmeta.ZabbixCredential, error) {
			return assetmeta.ZabbixCredential{Method: "token", Secret: "test-secret"}, nil
		},
		ClusterBinding: func(_ context.Context, scope ZabbixScope, id string) (bool, error) {
			return scope.Environment == "production" && id == "ack-prod", nil
		},
	}
	f.server.registerZabbix(f.mux)
	endpoint := "https://zabbix.example/api_jsonrpc.php"
	test := f.request("POST", "/zabbix/test", "admin", requestJSON(t, zabbixTestInput{
		Endpoint: endpoint, Credential: assetmeta.ZabbixCredential{Method: "token", Secret: "test-secret"}}), "", "")
	checked := decoded[zabbixTestResult](t, test, 200)
	if strings.Contains(test.Body.String(), "test-secret") {
		t.Fatal("credential was echoed")
	}

	selection := zabbixSelection{Draft: checked.Draft, Scope: ZabbixScope{"production", "ops"}, GroupIDs: []string{"10"}}
	if code := f.request("POST", "/zabbix/sources", "readonly", requestJSON(t, selection), "", "").Code; code != 403 {
		t.Fatalf("unauthorized save: %d", code)
	}
	if code := f.request("POST", "/zabbix/preview", "admin", requestJSON(t, selection), "", "").Code; code != 200 {
		t.Fatalf("preview: %d", code)
	}
	created := decoded[ZabbixSource](t, f.request("POST", "/zabbix/sources", "admin", requestJSON(t, selection), "", ""), 201)
	if created.Health != "pending" || created.ProblemCount != nil || created.SyncedAt != nil {
		t.Fatalf("preview was published: %+v", created)
	}
	stored, err := zabbixByID(t.Context(), f.db.DB, created.ID)
	if err != nil || string(stored.credential) != "test-ciphertext" {
		t.Fatalf("unexpected credential storage %v", err)
	}
	sync := func(upstreamErr error) {
		t.Helper()
		c.err = upstreamErr
		path := "/zabbix/sources/" + created.ID + "/sync"
		decoded[map[string]bool](t, f.request("POST", path, "admin", "", "", ""), 202)
		job, err := f.server.Store.claimZabbix(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.server.Store.claimZabbix(t.Context()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("lease failed: %v", err)
		}
		f.server.syncZabbix(t.Context(), job)
	}
	type listing struct {
		Items   []zabbixProblemRow
		Sources []zabbixSourceHealth
	}
	read := func(query, actor string) listing {
		return decoded[listing](t, f.request("GET", "/zabbix/problems"+query, actor, "", "", ""), 200)
	}
	sync(context.DeadlineExceeded)
	if got := read("", "admin"); len(got.Items) != 0 || got.Sources[0].HasSnapshot || got.Sources[0].Health != "unavailable" {
		t.Fatalf("first failed sync: %+v", got)
	}
	c.snapshot.Problems = nil
	sync(nil)
	if got := read("", "admin"); len(got.Items) != 0 || !got.Sources[0].HasSnapshot || got.Sources[0].Health != "available" {
		t.Fatalf("healthy zero: %+v", got)
	}
	c.snapshot.Problems = []assetmeta.ZabbixProblem{row}
	sync(nil)
	if got := read("", "admin"); len(got.Items) != 1 || got.Items[0].Hosts[0].ClusterID != "ack-prod" || !strings.Contains(got.Items[0].OriginalURL, "eventid=20") {
		t.Fatalf("projected problems: %+v", got)
	}
	if got := read("?environmentId=production&businessId=different", "admin"); len(got.Items) != 0 || len(got.Sources) != 0 {
		t.Fatalf("wrong business scope: %+v", got)
	}
	if got := read("", "readonly"); len(got.Items) != 0 || len(got.Sources) != 0 {
		t.Fatalf("unauthorized snapshot: %+v", got)
	}
	c.snapshot.HostsVerified = false
	sync(context.DeadlineExceeded)
	if got := read("", "admin"); len(got.Items) != 1 || got.Sources[0].Health != "stale" || got.Sources[0].SyncedAt == nil {
		t.Fatalf("stale snapshot: %+v", got)
	}
	c.snapshot.HostsVerified = true
	c.snapshot.Hosts = nil
	sync(context.DeadlineExceeded)
	if got := read("", "admin"); len(got.Items) != 0 || got.Sources[0].Health != "stale" {
		t.Fatalf("withdrawn host retained: %+v", got)
	}
	c.snapshot.Hosts = []assetmeta.ZabbixHost{host}
	row.RecoveryID, row.RecoveryClock = "21", strconv.FormatInt(time.Now().Unix(), 10)
	c.snapshot.Problems = []assetmeta.ZabbixProblem{row}
	sync(nil)
	if got := read("?recent=true", "admin"); len(got.Items) != 1 || !got.Items[0].Recovered {
		t.Fatalf("confirmed recovery: %+v", got)
	}
	if got := read("", "admin"); len(got.Items) != 0 {
		t.Fatal("recovered event counted as active")
	}
	sync(assetmeta.ErrAuthorization)
	if got := read("?recent=true", "admin"); len(got.Items) != 0 || got.Sources[0].HasSnapshot || got.Sources[0].Health != "restricted" {
		t.Fatalf("restricted credentials retained data: %+v", got)
	}
	path := "/zabbix/sources/" + created.ID
	badPatch := f.request(http.MethodPatch, path, "admin", `{"enabled":false}`, "", `"99"`)
	if badPatch.Code != 412 {
		t.Fatalf("unexpected stale config update: %d", badPatch.Code)
	}
	paused := decoded[ZabbixSource](t, f.request(http.MethodPatch, path, "admin", `{"enabled":false}`, "", `"1"`), 200)
	if paused.Health != "paused" {
		t.Fatalf("unexpected pause state: %+v", paused)
	}
}
