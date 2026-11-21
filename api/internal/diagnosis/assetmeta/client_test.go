package assetmeta

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReadonlyAssetQueriesUseBoundCredentials(t *testing.T) {
	const endpoint = "https://zabbix.example/api_jsonrpc.php"
	const scope = "production/retail/ack-prod/checkout"
	dir := t.TempDir()
	for _, tc := range []struct {
		kind, endpoint, region, id, json string
	}{
		{Zabbix, endpoint, "", "10001", `{"jsonrpc":"2.0","result":[{"hostid":"10001","name":"worker","status":"0"},{"hostid":"999","name":"outside grant","status":"0"}],"id":1}`},
		{Aliyun, "", "cn-hangzhou", "i-example", `{"Instances":{"Instance":[{"InstanceId":"i-example","InstanceName":"queue","Status":"Running"}]}}`},
		{Tencent, "", "ap-guangzhou", "ins-example", `{"Response":{"InstanceSet":[{"InstanceId":"ins-example","InstanceName":"api","InstanceState":"RUNNING"}]}}`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			c := New(dir, []string{endpoint})
			keys := Credentials{Scope: scope, Kind: tc.kind, Endpoint: tc.endpoint, Region: tc.region, AssetIDs: []string{tc.id}, Purpose: "inventory",
				Token: "fixture-token", AccessKeyID: "fixture-key-id", AccessKeySecret: "fixture-key-value", SessionToken: "fixture-session"}
			raw, _ := json.Marshal(keys)
			if err := os.WriteFile(filepath.Join(dir, "fixture.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" {
					t.Fatal("provider request was not TLS")
				}
				switch tc.kind {
				case Zabbix:
					body, _ := io.ReadAll(r.Body)
					if r.URL.String() != endpoint || r.Header.Get("Authorization") != "Bearer fixture-token" ||
						!strings.Contains(string(body), `"method":"host.get"`) || !strings.Contains(string(body), `"hostids":["10001"]`) {
						t.Fatal("unexpected Zabbix host metadata request")
					}
				case Aliyun:
					query := r.URL.Query()
					if r.URL.Host != "ecs.cn-hangzhou.aliyuncs.com" || query.Get("Action") != "DescribeInstances" ||
						query.Get("InstanceIds") != `["i-example"]` || query.Get("Signature") == "" || query.Get("SecurityToken") != "fixture-session" {
						t.Fatal("unexpected Aliyun ECS metadata request")
					}
				case Tencent:
					body, _ := io.ReadAll(r.Body)
					if r.URL.Host != "cvm.tencentcloudapi.com" || r.Header.Get("X-TC-Action") != "DescribeInstances" ||
						r.Header.Get("X-TC-Region") != "ap-guangzhou" || !strings.HasPrefix(r.Header.Get("Authorization"), "TC3-HMAC-SHA256 Credential=fixture-key-id/") ||
						!strings.Contains(string(body), `"InstanceIds":["ins-example"]`) || r.Header.Get("X-TC-Token") != "fixture-session" {
						t.Fatal("unexpected Tencent CVM metadata request")
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.json)), Header: make(http.Header)}, nil
			})
			cfg := Config{Kind: tc.kind, Endpoint: tc.endpoint, Region: tc.region, AssetIDs: []string{tc.id}}
			if _, err := c.Assets(context.Background(), cfg, "fixture.json", "production/other/ack-prod/checkout"); !errors.Is(err, ErrCredentials) {
				t.Fatalf("cross-scope reference: %v", err)
			}
			rejected := cfg
			rejected.AssetIDs = []string{"different-id"}
			if _, err := c.Assets(context.Background(), rejected, "fixture.json", scope); !errors.Is(err, ErrCredentials) {
				t.Fatalf("outside credential allowlist: %v", err)
			}
			if calls != 0 {
				t.Fatal("invalid binding reached upstream")
			}
			out, err := c.Assets(context.Background(), cfg, "fixture.json", scope)
			if err != nil || len(out) != 1 || out[0].ExternalID != tc.id || calls != 1 {
				t.Fatalf("provider metadata: %+v, %d calls, %v", out, calls, err)
			}
			if _, err = c.IngressToken("fixture.json", scope, cfg); !errors.Is(err, ErrCredentials) {
				t.Fatalf("inventory credential was usable as webhook credential: %v", err)
			}
		})
	}
}

func TestCredentialsCannotEscapeMountOrUseUnknownEndpoint(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(outside, []byte(`{"token":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside.json")); err != nil {
		t.Fatal(err)
	}
	c := New(root, []string{"https://approved.example/api_jsonrpc.php"})
	if _, err := c.ReadCredentials("outside.json"); !errors.Is(err, ErrCredentials) {
		t.Fatalf("outside symlink accepted: %v", err)
	}
	for _, endpoint := range []string{"http://approved.example/api_jsonrpc.php", "https://other.example/api_jsonrpc.php", "https://approved.example/api_jsonrpc.php?q=1"} {
		if err := c.Validate(Config{Kind: Zabbix, Endpoint: endpoint, AssetIDs: []string{"10001"}}); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("unapproved endpoint accepted: %v", err)
		}
	}
}
