package assetmeta

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestZabbix6CredentialsAndProblemScope(t *testing.T) {
	const endpoint = "https://zabbix.example/zabbix/api_jsonrpc.php"
	for _, test := range []struct{ method, version string }{{"token", "6.0.36"}, {"password", "6.2.9"}, {"token", "6.4.21"}} {
		t.Run(test.version, func(t *testing.T) {
			client := New("", []string{endpoint})
			calls := map[string]int{}
			client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				var q struct {
					Method string
					Auth   string
					Params json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Fatal(err)
				}
				if r.URL.String() != endpoint || r.Header.Get("Authorization") != "" {
					t.Fatal("unexpected request destination or authentication header")
				}
				calls[q.Method]++
				result := `[]`
				switch q.Method {
				case "apiinfo.version":
					if q.Auth != "" {
						t.Fatal("version request carried credentials")
					}
					result = `"` + test.version + `"`
				case "user.login":
					if test.method != "password" || q.Auth != "" || !strings.Contains(string(q.Params), `"username":"svc"`) {
						t.Fatal("invalid login")
					}
					result = `"session"`
				case "user.logout":
					if q.Auth != "session" {
						t.Fatal("wrong session to close")
					}
					result = `true`
				default:
					if test.method == "token" && q.Auth != "fixture-secret" || test.method == "password" && q.Auth != "session" {
						t.Fatal("invalid 6.x auth")
					}
					switch q.Method {
					case "hostgroup.get":
						result = `[{"groupid":"10","name":"Windows"}]`
					case "host.get":
						result = `[{"hostid":"42","name":"Windows","tags":[{"tag":"kube-aiops.cluster-id","value":"ack"}]}]`
					case "problem.get":
						result = `[{"eventid":"60","objectid":"43","clock":"1770000000","name":"memory low","severity":"4","r_eventid":"0","suppressed":"0"}]`
					case "event.get":
						if strings.Contains(string(q.Params), `"eventids":["61"]`) {
							result = `[{"eventid":"61","clock":"1770000050"}]`
						} else if strings.Contains(string(q.Params), `"eventids":`) {
							result = `[{"eventid":"60","hosts":[{"hostid":"42","name":"Windows"},{"hostid":"43","name":"private"}]},
								{"eventid":"59","name":"Recovered","clock":"1770000000","r_eventid":"61","hosts":[{"hostid":"42","name":"Windows"}]}]`
						}
					default:
						t.Fatal("unexpected Zabbix operation")
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`))}, nil
			})
			keys := ZabbixCredential{Method: test.method, Secret: "fixture-secret"}
			if test.method == "password" {
				keys.Username = "svc"
			}
			version, groups, err := client.TestZabbix(t.Context(), "https://zabbix.example/zabbix", keys)
			if err != nil || version != test.version || len(groups) != 1 {
				t.Fatalf("test result %s %+v %v", version, groups, err)
			}
			got, err := client.ZabbixProblems(t.Context(), endpoint, keys, []string{"10"}, []string{"59"})
			if err != nil || len(got.Problems) != 2 || len(got.Hosts) != 1 || !got.HostsVerified {
				t.Fatalf("bad snapshot: %+v %v", got, err)
			}
			if len(got.Problems[0].Hosts) != 1 || got.Problems[0].Hosts[0].ID != "42" || got.Problems[1].RecoveryClock != "1770000050" {
				t.Fatalf("host or recovery validation failed: %+v", got.Problems)
			}
			if calls["user.login"] != calls["user.logout"] {
				t.Fatal("login session was left open")
			}
			if _, err = client.ZabbixProblems(t.Context(), endpoint, keys, []string{"99"}, nil); !errors.Is(err, ErrZabbixScope) {
				t.Fatalf("missing authorized group not distinguished: %v", err)
			}
		})
	}
}

func TestZabbixRejectsUnexpectedEndpointBeforeSendingCredentials(t *testing.T) {
	client := New("", []string{"https://approved.example/api_jsonrpc.php"})
	client.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached network")
		return nil, nil
	})
	for _, endpoint := range []string{"http://approved.example", "https://different.example", "https://approved.example/?x=1", "https://approved.example/a/../api_jsonrpc.php", "https://user@approved.example"} {
		_, _, err := client.TestZabbix(context.Background(), endpoint, ZabbixCredential{Method: "token", Secret: "fixture"})
		if !errors.Is(err, ErrConfiguration) {
			t.Fatalf("%s: %v", endpoint, err)
		}
	}
}
