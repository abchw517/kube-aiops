package assetmeta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

const zabbixLimit = 500

var (
	ErrZabbixTLS            = errors.New("Zabbix TLS certificate invalid")
	ErrZabbixAuthentication = fmt.Errorf("Zabbix authentication rejected: %w", ErrAuthorization)
	ErrZabbixVersion        = errors.New("unsupported Zabbix version")
	ErrZabbixScope          = errors.New("Zabbix groups or hosts unavailable")
	ErrZabbixLimit          = errors.New("Zabbix result exceeds collection limit")
)

type ZabbixCredential struct {
	Method   string `json:"method"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret"`
}

func (c ZabbixCredential) Valid() bool {
	return c.Secret != "" && len(c.Secret) <= 4096 && !strings.ContainsAny(c.Secret, "\r\n\x00") &&
		(c.Method == "token" && c.Username == "" || c.Method == "password" && strings.TrimSpace(c.Username) != "" &&
			len(c.Username) <= 255 && !strings.ContainsAny(c.Username, "\r\n\x00"))
}

type ZabbixGroup struct {
	ID   string `json:"groupid"`
	Name string `json:"name"`
}

type ZabbixHost struct {
	ClusterID string      `json:"clusterId,omitempty"`
	Tags      []ZabbixTag `json:"tags,omitempty"`
	ID        string      `json:"hostid"`
	Name      string      `json:"name"`
}

type ZabbixTag struct {
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

type ZabbixProblem struct {
	TriggerID     string       `json:"objectid"`
	ID            string       `json:"eventid"`
	Name          string       `json:"name"`
	Severity      string       `json:"severity"`
	Acknowledged  string       `json:"acknowledged"`
	Suppressed    string       `json:"suppressed"`
	Cause         string       `json:"cause_eventid"`
	Clock         string       `json:"clock"`
	RecoveryID    string       `json:"r_eventid"`
	RecoveryClock string       `json:"r_clock"`
	Tags          []ZabbixTag  `json:"tags,omitempty"`
	Hosts         []ZabbixHost `json:"hosts"`
}

type ZabbixSnapshot struct {
	HostsVerified     bool            `json:"-"`
	HistoricTruncated bool            `json:"historicTruncated,omitempty"`
	Hosts             []ZabbixHost    `json:"hosts"`
	Problems          []ZabbixProblem `json:"problems"`
}

func (c *Client) ZabbixEndpoint(value string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u == nil || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.User != nil ||
		u.Fragment != "" || u.Scheme != "https" || u.Hostname() == "" {
		return "", ErrConfiguration
	}
	root := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(root, "/api_jsonrpc.php") {
		if strings.Contains(path.Base(root), ".") {
			return "", ErrConfiguration
		}
		root += "/api_jsonrpc.php"
	}
	u.Path = root
	endpoint := u.String()
	if !c.hosts[endpoint] {
		return "", ErrConfiguration
	}
	return endpoint, nil
}

type ZabbixStageError struct {
	Stage string
	Err   error
}

func (e ZabbixStageError) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e ZabbixStageError) Unwrap() error { return e.Err }

func (c *Client) zabbixRPC(ctx context.Context, endpoint, method, auth string, params any, result any) error {
	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	if auth != "" {
		request["auth"] = auth
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	req, err := jsonRequest(ctx, endpoint, body)
	if err != nil {
		return ErrConfiguration
	}
	req.Header.Set("Content-Type", "application/json-rpc")
	var response struct {
		Result json.RawMessage
		Error  *struct {
			Code    int
			Message string
			Data    string
		}
	}
	if err := c.request(req, &response); err != nil {
		return err
	}
	if response.Error != nil {
		msg := strings.ToLower(response.Error.Message + " " + response.Error.Data)
		for _, reason := range []string{"not authorized", "re-login", "session terminated", "session expired", "login name", "password", "relogin"} {
			if strings.Contains(msg, reason) {
				return ErrZabbixAuthentication
			}
		}
		for _, reason := range []string{"permission", "not allowed", "no permissions"} {
			if strings.Contains(msg, reason) {
				return ErrAuthorization
			}
		}
		if method == "user.login" && response.Error.Code == -32602 {
			return ErrAuthorization
		}
		return ErrUnavailable
	}
	if len(response.Result) == 0 || string(response.Result) == "null" || json.Unmarshal(response.Result, result) != nil {
		return ErrUnavailable
	}
	return nil
}

type zabbixSession struct {
	client         *Client
	endpoint, auth string
}

func (c *Client) withZabbix(ctx context.Context, raw string, credential ZabbixCredential, fn func(zabbixSession, string) error) error {
	endpoint, err := c.ZabbixEndpoint(raw)
	if err != nil {
		return ZabbixStageError{"endpoint", err}
	}
	if !credential.Valid() {
		return ZabbixStageError{"authentication", ErrCredentials}
	}
	var version string
	if err := c.zabbixRPC(ctx, endpoint, "apiinfo.version", "", []any{}, &version); err != nil {
		return ZabbixStageError{"endpoint", err}
	}
	parts := strings.Split(version, ".")
	if len(parts) < 3 || parts[0] != "6" || !slices.Contains([]string{"0", "2", "4"}, parts[1]) {
		return ZabbixStageError{"endpoint", ErrZabbixVersion}
	}
	auth := credential.Secret
	if credential.Method == "password" {
		err := c.zabbixRPC(ctx, endpoint, "user.login", "", map[string]string{"username": credential.Username, "password": credential.Secret}, &auth)
		if err != nil {
			return ZabbixStageError{"authentication", err}
		}
		if auth == "" {
			return ZabbixStageError{"authentication", ErrAuthorization}
		}
		defer func() {
			logout, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			var ok bool
			_ = c.zabbixRPC(logout, endpoint, "user.logout", auth, []any{}, &ok)
		}()
	}
	return fn(zabbixSession{c, endpoint, auth}, version)
}

func (s zabbixSession) call(ctx context.Context, method string, params any, result any) error {
	return s.client.zabbixRPC(ctx, s.endpoint, method, s.auth, params, result)
}

func (s zabbixSession) groups(ctx context.Context, groupIDs []string) ([]ZabbixGroup, error) {
	params := map[string]any{"output": []string{"groupid", "name"}, "sortfield": "name", "limit": zabbixLimit + 1}
	if len(groupIDs) > 0 {
		params["groupids"] = groupIDs
	}
	var groups []ZabbixGroup
	if err := s.call(ctx, "hostgroup.get", params, &groups); err != nil {
		return nil, err
	}
	if len(groups) > zabbixLimit {
		return nil, ErrZabbixLimit
	}
	return groups, nil
}

func (c *Client) TestZabbix(ctx context.Context, endpoint string, credential ZabbixCredential) (string, []ZabbixGroup, error) {
	var version string
	var groups []ZabbixGroup
	err := c.withZabbix(ctx, endpoint, credential, func(s zabbixSession, v string) error {
		var hosts []ZabbixHost
		if err := s.call(ctx, "host.get", map[string]any{"output": []string{"hostid"}, "limit": 1}, &hosts); err != nil {
			stage := "groups"
			if errors.Is(err, ErrAuthorization) && errors.Is(err, ErrZabbixAuthentication) {
				stage = "authentication"
			}
			return ZabbixStageError{stage, err}
		}
		if len(hosts) == 0 {
			return ZabbixStageError{"groups", ErrZabbixScope}
		}
		var err error
		groups, err = s.groups(ctx, nil)
		if err != nil {
			return ZabbixStageError{"groups", err}
		}
		if len(groups) == 0 {
			return ZabbixStageError{"groups", ErrZabbixScope}
		}
		for _, method := range []string{"problem.get", "event.get"} {
			var rows []ZabbixProblem
			if err := s.call(ctx, method, map[string]any{"output": []string{"eventid"}, "hostids": []string{hosts[0].ID}, "source": 0, "object": 0, "limit": 1}, &rows); err != nil {
				return ZabbixStageError{"problems", err}
			}
		}
		version = v
		return nil
	})
	return version, groups, err
}

func (c *Client) ZabbixProblems(ctx context.Context, endpoint string, credential ZabbixCredential, groupIDs, previousActive []string) (ZabbixSnapshot, error) {
	snapshot := ZabbixSnapshot{Hosts: []ZabbixHost{}, Problems: []ZabbixProblem{}}
	err := c.withZabbix(ctx, endpoint, credential, func(s zabbixSession, _ string) error {
		groups, err := s.groups(ctx, groupIDs)
		if err != nil {
			return ZabbixStageError{"groups", err}
		}
		for _, id := range groupIDs {
			if !slices.ContainsFunc(groups, func(g ZabbixGroup) bool { return g.ID == id }) {
				return ZabbixStageError{"groups", ErrZabbixScope}
			}
		}
		if err := s.call(ctx, "host.get", map[string]any{"output": []string{"hostid", "name"}, "selectTags": []string{"tag", "value"}, "groupids": groupIDs, "monitored_hosts": true, "limit": zabbixLimit + 1}, &snapshot.Hosts); err != nil {
			return ZabbixStageError{"groups", err}
		}
		if len(snapshot.Hosts) > zabbixLimit {
			return ZabbixStageError{"groups", ErrZabbixLimit}
		}
		snapshot.HostsVerified = true
		if len(snapshot.Hosts) == 0 {
			return nil
		}
		snapshot.Problems, err = s.problems(ctx, snapshot.Hosts, previousActive)
		if err != nil {
			return ZabbixStageError{"problems", err}
		}
		return nil
	})
	return snapshot, err
}

func (s zabbixSession) problems(ctx context.Context, hosts []ZabbixHost, previousActive []string) ([]ZabbixProblem, error) {
	hostIDs := make([]string, 0, len(hosts))
	allowed := map[string]bool{}
	for _, host := range hosts {
		if !allowed[host.ID] {
			hostIDs = append(hostIDs, host.ID)
			allowed[host.ID] = true
		}
	}
	var rows []ZabbixProblem
	params := map[string]any{"output": "extend", "source": 0, "object": 0, "hostids": hostIDs,
		"selectTags": []string{"tag", "value"}, "recent": true, "limit": zabbixLimit + 1}
	if err := s.call(ctx, "problem.get", params, &rows); err != nil {
		return nil, err
	}
	if len(rows) > zabbixLimit {
		return nil, ErrZabbixLimit
	}
	byID := make(map[string]ZabbixProblem, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	eventIDs := make([]string, 0, len(rows)+len(previousActive))
	eventIDs = append(eventIDs, previousActive...)
	for _, row := range rows {
		if !slices.Contains(eventIDs, row.ID) {
			eventIDs = append(eventIDs, row.ID)
		}
	}
	if len(eventIDs) == 0 {
		return []ZabbixProblem{}, nil
	}

	var enriched []ZabbixProblem
	for offset := 0; offset < len(eventIDs); offset += 100 {
		var batch []ZabbixProblem
		query := map[string]any{"output": "extend", "source": 0, "object": 0, "value": 1, "eventids": eventIDs[offset:min(offset+100, len(eventIDs))], "hostids": hostIDs,
			"selectHosts": []string{"hostid", "name"}, "selectTags": []string{"tag", "value"}}
		if err := s.call(ctx, "event.get", query, &batch); err != nil {
			return nil, err
		}
		enriched = append(enriched, batch...)
	}
	recoveryIDs := []string{}
	result := make([]ZabbixProblem, 0, len(enriched))
	for _, event := range enriched {
		problem, current := byID[event.ID]
		if current {
			problem.Hosts = event.Hosts
			if event.RecoveryID != "" && event.RecoveryID != "0" {
				problem.RecoveryID = event.RecoveryID
			}
		} else {
			if event.RecoveryID == "" || event.RecoveryID == "0" {
				continue
			}
			problem = event
		}
		problem.Hosts = slices.DeleteFunc(problem.Hosts, func(host ZabbixHost) bool { return !allowed[host.ID] })
		if len(problem.Hosts) == 0 {
			continue
		}
		if problem.RecoveryID != "" && problem.RecoveryID != "0" && (problem.RecoveryClock == "" || problem.RecoveryClock == "0") {
			recoveryIDs = append(recoveryIDs, problem.RecoveryID)
		}
		result = append(result, problem)
		delete(byID, event.ID)
	}
	if len(byID) > 0 {
		// Publish only a complete observation.
		return nil, ErrUnavailable
	}
	if len(recoveryIDs) > 0 {
		var recovered []ZabbixProblem
		if err := s.call(ctx, "event.get", map[string]any{"output": []string{"eventid", "clock"}, "source": 0, "object": 0, "eventids": recoveryIDs, "hostids": hostIDs}, &recovered); err != nil {
			return nil, err
		}
		recoveredAt := map[string]string{}
		for _, row := range recovered {
			recoveredAt[row.ID] = row.Clock
		}
		for i, row := range result {
			if row.RecoveryClock == "" || row.RecoveryClock == "0" {
				result[i].RecoveryClock = recoveredAt[row.RecoveryID]
			}
		}
	}
	return result, nil
}

func (p ZabbixProblem) OccurredAt() (time.Time, error) {
	n, err := strconv.ParseInt(p.Clock, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("invalid event timestamp")
	}
	return time.Unix(n, 0).UTC(), nil
}

func (p ZabbixProblem) Recovered() bool { return p.RecoveryID != "" && p.RecoveryID != "0" }
func (p ZabbixProblem) SuppressedOrSymptom() bool {
	return p.Suppressed == "1" || p.Cause != "" && p.Cause != "0"
}
