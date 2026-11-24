package incident

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

type ZabbixCollector interface {
	ZabbixEndpoint(string) (string, error)
	TestZabbix(context.Context, string, assetmeta.ZabbixCredential) (string, []assetmeta.ZabbixGroup, error)
	ZabbixProblems(context.Context, string, assetmeta.ZabbixCredential, []string, []string) (assetmeta.ZabbixSnapshot, error)
}

type ZabbixIntegration struct {
	Collector      ZabbixCollector
	Authorize      func(context.Context, identity.Principal, string, ZabbixScope) (bool, error)
	ScopeActive    func(context.Context, ZabbixScope) (bool, error)
	ClusterBinding func(context.Context, ZabbixScope, string) (bool, error)
	Catalog        func(context.Context) (ZabbixScopeOptions, error)
	Seal           func(context.Context, string, string, assetmeta.ZabbixCredential) ([]byte, error)
	Open           func(string, string, []byte) (assetmeta.ZabbixCredential, error)

	mu     sync.Mutex
	drafts map[string]zabbixDraft
}

type zabbixDraft struct {
	owner             string
	sourceID          string
	revision          int64
	endpoint, version string
	credential        assetmeta.ZabbixCredential
	groups            []assetmeta.ZabbixGroup
	expires           time.Time
}

func zabbixActor(p identity.Principal) string {
	return fmt.Sprintf("%s\x00%s\x00%s", p.Provider, p.Subject, p.SessionVersion)
}

func (z *ZabbixIntegration) draft(p identity.Principal, id string) (zabbixDraft, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	item := z.drafts[id]
	if item.owner != zabbixActor(p) || !item.expires.After(time.Now()) {
		return zabbixDraft{}, httpError{409, "ZABBIX_TEST_EXPIRED", "连接验证已失效，请重新测试。"}
	}
	return item, nil
}

func (z *ZabbixIntegration) saveDraft(d zabbixDraft) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.drafts == nil {
		z.drafts = map[string]zabbixDraft{}
	}
	for id, item := range z.drafts {
		if !item.expires.After(time.Now()) || (item.owner == d.owner && item.sourceID == d.sourceID) {
			delete(z.drafts, id)
		}
	}
	if len(z.drafts) >= 128 {
		return "", httpError{429, "ZABBIX_TEST_BUSY", "当前连接测试较多，请稍后重试。"}
	}
	id := zabbixID()
	z.drafts[id] = d
	return id, nil
}

func (z *ZabbixIntegration) expireDrafts(now time.Time) {
	z.mu.Lock()
	defer z.mu.Unlock()
	for id, item := range z.drafts {
		if !item.expires.After(now) {
			delete(z.drafts, id)
		}
	}
}

func (z *ZabbixIntegration) clearDraft(draft string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	delete(z.drafts, draft)
}

func (s *Server) registerZabbix(mux *http.ServeMux) {
	if s.Zabbix == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/diagnostics/zabbix/scopes", s.protect(s.zabbixScopes))
	mux.HandleFunc("GET /api/v1/diagnostics/zabbix/sources", s.protect(s.zabbixSources))
	mux.HandleFunc("POST /api/v1/diagnostics/zabbix/sources", s.protect(s.createZabbix))
	mux.HandleFunc("PATCH /api/v1/diagnostics/zabbix/sources/{sourceId}", s.protect(s.updateZabbix))
	mux.HandleFunc("POST /api/v1/diagnostics/zabbix/sources/{sourceId}/sync", s.protect(s.queueZabbix))
	mux.HandleFunc("POST /api/v1/diagnostics/zabbix/test", s.protect(s.testZabbix))
	mux.HandleFunc("POST /api/v1/diagnostics/zabbix/preview", s.protect(s.previewZabbix))
	mux.HandleFunc("GET /api/v1/diagnostics/zabbix/problems", s.protect(s.zabbixProblems))
}

var zabbixBadInput = httpError{400, "ZABBIX_INVALID_INPUT", "请核对 Zabbix 地址、认证信息和采集范围。"}

func zabbixFailure(err error) error {
	var stage assetmeta.ZabbixStageError
	code := "upstream"
	if errors.As(err, &stage) {
		code = stage.Stage
	}
	switch {
	case errors.Is(err, assetmeta.ErrZabbixEndpointsUnconfigured):
		return httpError{503, "ZABBIX_ENDPOINTS_UNCONFIGURED", "平台尚未配置 Zabbix 允许地址。请在 API 容器的 DIAGNOSTIC_ZABBIX_ENDPOINTS 中配置此 Zabbix 的 API 地址。"}
	case errors.Is(err, assetmeta.ErrZabbixEndpointNotAllowed):
		return httpError{403, "ZABBIX_ENDPOINT_NOT_ALLOWED", "此 Zabbix 地址未在平台允许列表中。请核对提交的地址，或将对应的 API 地址加入 DIAGNOSTIC_ZABBIX_ENDPOINTS。"}
	case errors.Is(err, assetmeta.ErrConfiguration):
		return httpError{400, "ZABBIX_ENDPOINT", "请输入有效的 HTTPS Zabbix 站点根地址或完整的 api_jsonrpc.php 地址。"}
	case errors.Is(err, assetmeta.ErrZabbixVersion):
		return httpError{400, "ZABBIX_VERSION", "此接入流程支持 Zabbix 6.0、6.2 与 6.4。"}
	case errors.Is(err, assetmeta.ErrZabbixScope):
		return httpError{422, "ZABBIX_GROUPS", "未获得所选主机组的读取权限。"}
	case errors.Is(err, assetmeta.ErrCredentials) || errors.Is(err, assetmeta.ErrZabbixAuthentication) || code == "authentication":
		return httpError{422, "ZABBIX_AUTHENTICATION", "Zabbix 认证失败，请核对账号密码或 API Token。"}
	case errors.Is(err, assetmeta.ErrAuthorization):
		if code == "groups" {
			return httpError{422, "ZABBIX_GROUPS", "无法读取主机组，请检查账号的只读范围。"}
		}
		return httpError{422, "ZABBIX_PROBLEM_PERMISSION", "无法读取问题，请检查 problem.get 和 event.get 权限。"}
	case errors.Is(err, assetmeta.ErrZabbixLimit):
		return httpError{422, "ZABBIX_COLLECTION_LIMIT", "结果超过采集上限，请缩小主机组范围。"}
	case errors.Is(err, context.DeadlineExceeded):
		return httpError{504, "ZABBIX_TIMEOUT", "Zabbix 请求超时，请检查平台到 Zabbix 的网络。"}
	case errors.Is(err, assetmeta.ErrZabbixTLS):
		return httpError{502, "ZABBIX_TLS", "Zabbix TLS 证书校验失败，请核对域名、证书链和平台信任配置。"}
	default:
		return httpError{502, "ZABBIX_UPSTREAM", "Zabbix 暂时不可用，请检查网络和 API 地址。"}
	}
}

func (s *Server) requireZabbix(ctx context.Context, p identity.Principal, code string, scope ZabbixScope) error {
	if !validID(scope.Environment) || !validID(scope.Business) {
		return zabbixBadInput
	}
	ok, err := s.Zabbix.Authorize(ctx, p, code, scope)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

func (s *Server) sourceZabbix(ctx context.Context, p identity.Principal, id, code string) (zabbixSourceRecord, error) {
	d, err := zabbixByID(ctx, s.Store.db, id)
	if err != nil {
		return d, err
	}
	err = s.requireZabbix(ctx, p, code, d.Scope)
	if errors.Is(err, ErrForbidden) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *Server) zabbixScopes(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	if err := s.require(ctx, p, ConnectorWrite, Scope{}, true); err != nil {
		return err
	}
	options, err := s.Zabbix.Catalog(ctx)
	if err != nil {
		return err
	}
	// Return allowed pairs so a user's allowed business can vary by environment.
	type scopeChoice struct {
		ZabbixOption
		Businesses []ZabbixOption `json:"businesses"`
	}
	environments := []scopeChoice{}
	for _, env := range options.Environments {
		item := scopeChoice{ZabbixOption: env, Businesses: []ZabbixOption{}}
		for _, business := range options.Businesses {
			ok, err := s.Zabbix.Authorize(ctx, p, ConnectorWrite, ZabbixScope{env.ID, business.ID})
			if err != nil {
				return err
			}
			if ok {
				item.Businesses = append(item.Businesses, business)
			}
		}
		if len(item.Businesses) > 0 {
			environments = append(environments, item)
		}
	}
	adminhttp.WriteJSON(w, map[string]any{"environments": environments})
	return nil
}

func (s *Server) zabbixSources(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	if err := s.require(r.Context(), p, ConnectorRead, Scope{}, true); err != nil {
		return err
	}
	items := []ZabbixSource{}
	err := s.visitZabbix(r.Context(), p, func(d zabbixSourceRecord) (bool, error) {
		items = append(items, d.ZabbixSource)
		return true, nil
	})
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, map[string]any{"items": items})
	return nil
}

func (s *Server) visitZabbix(ctx context.Context, p identity.Principal, visit func(zabbixSourceRecord) (bool, error)) error {
	options, err := s.Zabbix.Catalog(ctx)
	if err != nil {
		return err
	}
	labels := func(id string, entries []ZabbixOption) string {
		for _, item := range entries {
			if item.ID == id {
				return item.Name
			}
		}
		return id
	}
	return scanConfigPages(ctx, s.Store.db, "diagnostic_zabbix_source", zabbixColumns, "", scanZabbix, func(d zabbixSourceRecord) (bool, error) {
		ok, err := s.Zabbix.Authorize(ctx, p, ConnectorRead, d.Scope)
		if err != nil || !ok {
			return true, err
		}
		d.EnvironmentName, d.BusinessName = labels(d.Scope.Environment, options.Environments), labels(d.Scope.Business, options.Businesses)
		return visit(d)
	})
}

type zabbixTestInput struct {
	Endpoint   string                     `json:"endpoint"`
	Credential assetmeta.ZabbixCredential `json:"credential"`
	SourceID   string                     `json:"sourceId,omitempty"`
}

type zabbixTestResult struct {
	Version  string                  `json:"version"`
	Endpoint string                  `json:"endpoint"`
	Groups   []assetmeta.ZabbixGroup `json:"groups"`
	Draft    string                  `json:"draft"`
}

func (s *Server) testZabbix(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	if err := s.require(ctx, p, ConnectorTest, Scope{}, true); err != nil {
		return err
	}
	var input zabbixTestInput
	if !adminhttp.Decode(w, r, &input) || !input.Credential.Valid() && (input.SourceID == "" || input.Credential.Method != "") {
		return zabbixBadInput
	}
	var revision int64
	if input.SourceID != "" {
		d, err := s.sourceZabbix(ctx, p, input.SourceID, ConnectorWrite)
		if err != nil {
			return err
		}
		if err = s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
			return err
		}
		input.Endpoint, revision = d.Endpoint, d.Version
		if input.Credential.Method == "" {
			input.Credential, err = s.Zabbix.Open(d.ID, d.Endpoint, d.credential)
			if err != nil {
				return zabbixFailure(err)
			}
			query, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			version, groups, err := s.Zabbix.Collector.TestZabbix(query, d.Endpoint, input.Credential)
			if err != nil {
				return zabbixFailure(err)
			}
			for i := range groups {
				groups[i].Name = cleanText(groups[i].Name, 160)
			}
			if err = s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
				return err
			}
			if err = s.requireZabbix(ctx, p, ConnectorWrite, d.Scope); err != nil {
				return err
			}
			adminhttp.WriteJSON(w, zabbixTestResult{Version: version, Endpoint: d.Endpoint, Groups: groups})
			return nil
		}
	}
	endpoint, err := s.Zabbix.Collector.ZabbixEndpoint(input.Endpoint)
	if err != nil {
		return zabbixFailure(err)
	}
	query, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	version, groups, err := s.Zabbix.Collector.TestZabbix(query, endpoint, input.Credential)
	if err != nil {
		return zabbixFailure(err)
	}
	for i := range groups {
		groups[i].Name = cleanText(groups[i].Name, 160)
	}
	id, err := s.Zabbix.saveDraft(zabbixDraft{
		owner: zabbixActor(p), sourceID: input.SourceID, revision: revision, endpoint: endpoint, version: version,
		credential: input.Credential, groups: groups, expires: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, zabbixTestResult{version, endpoint, groups, id})
	return nil
}

type zabbixSelection struct {
	Draft    string      `json:"draft,omitempty"`
	SourceID string      `json:"sourceId,omitempty"`
	Scope    ZabbixScope `json:"scope"`
	GroupIDs []string    `json:"groupIds"`
}

type zabbixChange struct {
	Draft    string    `json:"draft,omitempty"`
	GroupIDs *[]string `json:"groupIds,omitempty"`
	Enabled  *bool     `json:"enabled,omitempty"`
}

func validZabbixGroups(ids []string) bool {
	if len(ids) == 0 || len(ids) > 50 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		value, err := strconv.ParseUint(id, 10, 64)
		if err != nil || value == 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func zabbixGroupSelection(ids []string, known []assetmeta.ZabbixGroup) ([]assetmeta.ZabbixGroup, error) {
	if !validZabbixGroups(ids) {
		return nil, zabbixBadInput
	}
	out := make([]assetmeta.ZabbixGroup, 0, len(ids))
	for _, id := range ids {
		index := slices.IndexFunc(known, func(group assetmeta.ZabbixGroup) bool { return group.ID == id })
		if index < 0 {
			return nil, zabbixBadInput
		}
		out = append(out, known[index])
	}
	return out, nil
}

func zabbixGroupIDs(groups []assetmeta.ZabbixGroup) []string {
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		out = append(out, group.ID)
	}
	return out
}

func (s *Server) previewZabbix(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	var input zabbixSelection
	if !adminhttp.Decode(w, r, &input) || !validZabbixGroups(input.GroupIDs) {
		return zabbixBadInput
	}
	d, credential, err := s.zabbixSelection(ctx, p, input)
	if err != nil {
		return err
	}
	if err = s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
		return err
	}
	query, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	snapshot, err := s.Zabbix.Collector.ZabbixProblems(query, d.Endpoint, credential, input.GroupIDs, nil)
	if err != nil {
		return zabbixFailure(err)
	}
	if err = s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
		return err
	}
	rows := projectZabbix(d, snapshot.Problems, false)
	adminhttp.WriteJSON(w, map[string]any{"hostCount": len(snapshot.Hosts), "problemCount": len(rows), "problems": rows[:min(5, len(rows))]})
	return nil
}

func (s *Server) zabbixSelection(ctx context.Context, p identity.Principal, input zabbixSelection) (zabbixSourceRecord, assetmeta.ZabbixCredential, error) {
	var d zabbixSourceRecord
	var credential assetmeta.ZabbixCredential
	var err error
	if input.SourceID != "" {
		d, err = s.sourceZabbix(ctx, p, input.SourceID, ConnectorWrite)
		if err != nil {
			return d, credential, err
		}
	} else {
		d.Scope = input.Scope
		if err = s.requireZabbix(ctx, p, ConnectorWrite, d.Scope); err != nil {
			return d, credential, err
		}
	}
	if input.Draft != "" {
		draft, err := s.Zabbix.draft(p, input.Draft)
		if err != nil {
			return d, credential, err
		}
		if draft.sourceID != d.ID || draft.revision != d.Version {
			return d, credential, ErrVersion
		}
		d.Endpoint, d.UpstreamVersion, d.AuthMethod = draft.endpoint, draft.version, draft.credential.Method
		d.Groups, err = zabbixGroupSelection(input.GroupIDs, draft.groups)
		return d, draft.credential, err
	}
	if d.ID == "" {
		return d, credential, zabbixBadInput
	}
	if err = s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
		return d, credential, err
	}
	credential, err = s.Zabbix.Open(d.ID, d.Endpoint, d.credential)
	return d, credential, err
}

func (s *Server) createZabbix(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	var input zabbixSelection
	if !adminhttp.Decode(w, r, &input) || input.SourceID != "" || !validZabbixGroups(input.GroupIDs) {
		return zabbixBadInput
	}
	d, credential, err := s.zabbixSelection(ctx, p, input)
	if err != nil {
		return err
	}
	if err = s.validateZabbixSelection(ctx, p, d, credential, input.GroupIDs); err != nil {
		return err
	}
	d.ID, d.Enabled = zabbixID(), true
	d.credential, err = s.Zabbix.Seal(ctx, d.ID, d.Endpoint, credential)
	if err != nil {
		return err
	}
	result, err := s.Store.saveZabbix(ctx, d, p.Subject, true, true)
	if err != nil {
		return err
	}
	s.Zabbix.clearDraft(input.Draft)
	w.WriteHeader(http.StatusCreated)
	adminhttp.WriteJSON(w, result)
	return nil
}

func (s *Server) validateZabbixSelection(ctx context.Context, p identity.Principal, d zabbixSourceRecord, credential assetmeta.ZabbixCredential, ids []string) error {
	if err := s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
		return err
	}
	query, cancel := context.WithTimeout(ctx, 22*time.Second)
	defer cancel()
	if _, err := s.Zabbix.Collector.ZabbixProblems(query, d.Endpoint, credential, ids, nil); err != nil {
		return zabbixFailure(err)
	}
	if err := s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
		return err
	}
	return s.requireZabbix(ctx, p, ConnectorWrite, d.Scope)
}

func (s *Server) updateZabbix(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	d, err := s.sourceZabbix(ctx, p, r.PathValue("sourceId"), ConnectorWrite)
	if err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	if version != d.Version {
		return ErrVersion
	}
	var input zabbixChange
	if !adminhttp.Decode(w, r, &input) || input.Draft == "" && input.Enabled == nil && input.GroupIDs == nil {
		return zabbixBadInput
	}
	if input.Enabled != nil {
		d.Enabled = *input.Enabled
	}
	recheck := input.GroupIDs != nil || input.Draft != ""
	if recheck {
		if err = s.requireZabbix(ctx, p, ConnectorTest, d.Scope); err != nil {
			return err
		}
		ids := zabbixGroupIDs(d.Groups)
		if input.GroupIDs != nil {
			ids = *input.GroupIDs
		}
		var credential assetmeta.ZabbixCredential
		var groupOptions []assetmeta.ZabbixGroup
		if input.Draft != "" {
			draft, err := s.Zabbix.draft(p, input.Draft)
			if err != nil {
				return err
			}
			if draft.sourceID != d.ID || draft.revision != d.Version {
				return ErrVersion
			}
			credential, groupOptions = draft.credential, draft.groups
			d.AuthMethod, d.UpstreamVersion = credential.Method, draft.version
		} else {
			credential, err = s.Zabbix.Open(d.ID, d.Endpoint, d.credential)
			if err != nil {
				return err
			}
			query, cancel := context.WithTimeout(ctx, 20*time.Second)
			_, groupOptions, err = s.Zabbix.Collector.TestZabbix(query, d.Endpoint, credential)
			cancel()
			if err != nil {
				return zabbixFailure(err)
			}
			for i := range groupOptions {
				groupOptions[i].Name = cleanText(groupOptions[i].Name, 160)
			}
		}
		d.Groups, err = zabbixGroupSelection(ids, groupOptions)
		if err != nil {
			return err
		}
		if err = s.validateZabbixSelection(ctx, p, d, credential, ids); err != nil {
			return err
		}
		if input.Draft != "" {
			d.credential, err = s.Zabbix.Seal(ctx, d.ID, d.Endpoint, credential)
			if err != nil {
				return err
			}
		}
	}
	result, err := s.Store.saveZabbix(ctx, d, p.Subject, false, recheck || !d.Enabled)
	if err != nil {
		return err
	}
	if input.Draft != "" {
		s.Zabbix.clearDraft(input.Draft)
	}
	adminhttp.WriteJSON(w, result)
	return nil
}

func (s *Server) queueZabbix(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	d, err := s.sourceZabbix(r.Context(), p, r.PathValue("sourceId"), ConnectorTest)
	if err != nil {
		return err
	}
	if err = s.Store.queueZabbix(r.Context(), d, p.Subject); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	adminhttp.WriteJSON(w, map[string]bool{"queued": true})
	return nil
}

type zabbixProblemRow struct {
	OriginalURL  string                 `json:"originalURL,omitempty"`
	ID           string                 `json:"id"`
	EventID      string                 `json:"eventId"`
	Title        string                 `json:"title"`
	Severity     string                 `json:"severity"`
	RawSeverity  int                    `json:"rawSeverity"`
	Scope        ZabbixScope            `json:"scope"`
	Hosts        []assetmeta.ZabbixHost `json:"hosts"`
	Acknowledged bool                   `json:"acknowledged"`
	Recovered    bool                   `json:"recovered"`
	StartedAt    time.Time              `json:"startedAt"`
	RecoveredAt  *time.Time             `json:"recoveredAt,omitempty"`
	Tags         []assetmeta.ZabbixTag  `json:"tags,omitempty"`
	SourceID     string                 `json:"sourceId"`
}

func projectZabbix(d zabbixSourceRecord, problems []assetmeta.ZabbixProblem, recent bool) []zabbixProblemRow {
	out := make([]zabbixProblemRow, 0, len(problems))
	for _, item := range problems {
		if item.SuppressedOrSymptom() || item.Recovered() && (!recent || !recentRecovery(item, nowUTC())) {
			continue
		}
		stamp, err := item.OccurredAt()
		if err != nil || !validID(item.ID) {
			continue
		}
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(d.Endpoint+"\x00"+item.ID)))
		level := "info"
		raw, _ := strconv.Atoi(item.Severity)
		if raw >= 4 {
			level = "critical"
		} else if raw >= 2 {
			level = "warning"
		}
		row := zabbixProblemRow{ID: id, EventID: item.ID, Title: cleanText(item.Name, 400), Severity: level, RawSeverity: raw, Scope: d.Scope,
			Acknowledged: item.Acknowledged == "1", Recovered: item.Recovered(), StartedAt: stamp, SourceID: d.ID, Hosts: []assetmeta.ZabbixHost{}}
		if n, err := strconv.ParseInt(item.RecoveryClock, 10, 64); err == nil && n > 0 && row.Recovered {
			t := time.Unix(n, 0).UTC()
			row.RecoveredAt = &t
		}
		trigger, triggerErr := strconv.ParseUint(item.TriggerID, 10, 64)
		event, eventErr := strconv.ParseUint(item.ID, 10, 64)
		if triggerErr == nil && eventErr == nil && trigger > 0 && event > 0 {
			if address, err := url.Parse(d.Endpoint); err == nil && address.Scheme == "https" && address.Host != "" {
				address.Path = strings.TrimSuffix(address.Path, "api_jsonrpc.php") + "tr_events.php"
				address.RawQuery = url.Values{"triggerid": {item.TriggerID}, "eventid": {item.ID}}.Encode()
				row.OriginalURL = address.String()
			}
		}
		for _, host := range item.Hosts {
			row.Hosts = append(row.Hosts, assetmeta.ZabbixHost{ID: host.ID, Name: cleanText(host.Name, 160), ClusterID: host.ClusterID})
		}
		for _, tag := range item.Tags[:min(len(item.Tags), 20)] {
			row.Tags = append(row.Tags, assetmeta.ZabbixTag{Tag: cleanText(tag.Tag, 80), Value: cleanText(tag.Value, 160)})
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b zabbixProblemRow) int {
		if n := b.StartedAt.Compare(a.StartedAt); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

type zabbixSourceHealth struct {
	EnvironmentName string      `json:"environmentName,omitempty"`
	BusinessName    string      `json:"businessName,omitempty"`
	ID              string      `json:"id"`
	Scope           ZabbixScope `json:"scope"`
	Health          string      `json:"health"`
	SyncedAt        *time.Time  `json:"syncedAt,omitempty"`
	HasSnapshot     bool        `json:"hasSnapshot"`
}

func (s *Server) zabbixProblems(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	if err := s.require(r.Context(), p, ConnectorRead, Scope{}, true); err != nil {
		return err
	}
	query := r.URL.Query()
	env, business := query.Get("environmentId"), query.Get("businessId")
	recent := query.Get("recent") == "true"
	rows := []zabbixProblemRow{}
	sources := []zabbixSourceHealth{}
	index := map[string]int{}
	truncated := false
	err := s.visitZabbix(r.Context(), p, func(d zabbixSourceRecord) (bool, error) {
		if env != "" && d.Scope.Environment != env || business != "" && d.Scope.Business != business {
			return true, nil
		}
		sources = append(sources, zabbixSourceHealth{ID: d.ID, Scope: d.Scope, Health: d.Health, SyncedAt: d.SyncedAt, HasSnapshot: d.snapshot != nil, EnvironmentName: d.EnvironmentName, BusinessName: d.BusinessName})
		if d.snapshot == nil || query.Get("metadataOnly") == "true" {
			return true, nil
		}
		if recent && d.snapshot.HistoricTruncated {
			truncated = true
		}
		for _, item := range projectZabbix(d, d.snapshot.Problems, recent) {
			if i, ok := index[item.ID]; ok {
				// Prefer current data over a duplicated observation from a stale connector.
				prior := rows[i]
				if d.Health == "available" {
					rows[i] = item
				}
				for _, host := range append(prior.Hosts, item.Hosts...) {
					if !slices.ContainsFunc(rows[i].Hosts, func(h assetmeta.ZabbixHost) bool { return h.ID == host.ID }) {
						rows[i].Hosts = append(rows[i].Hosts, host)
					}
				}
				continue
			}
			if len(rows) >= 2000 {
				truncated = true
				break
			}
			index[item.ID] = len(rows)
			rows = append(rows, item)
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, map[string]any{"items": rows, "sources": sources, "truncated": truncated})
	return nil
}

func (s *Server) runZabbix(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		d, err := s.Store.claimZabbix(ctx)
		if err == nil {
			s.syncZabbix(ctx, d)
			continue
		}
		if !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
			slog.WarnContext(ctx, "diagnostic Zabbix queue unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.Zabbix.expireDrafts(now)
		}
	}
}

func (s *Server) syncZabbix(ctx context.Context, d zabbixSourceRecord) {
	var snapshot assetmeta.ZabbixSnapshot
	var credential assetmeta.ZabbixCredential
	var err error
	if s.Zabbix.ScopeActive != nil {
		var active bool
		active, err = s.Zabbix.ScopeActive(ctx, d.Scope)
		if err == nil && !active {
			err = assetmeta.ErrZabbixScope
		}
	}
	if err == nil {
		credential, err = s.Zabbix.Open(d.ID, d.Endpoint, d.credential)
	}
	if err == nil {
		previous := []string{}
		if d.snapshot != nil {
			for _, row := range d.snapshot.Problems {
				if !row.Recovered() {
					previous = append(previous, row.ID)
				}
			}
		}
		query, cancel := context.WithTimeout(ctx, 35*time.Second)
		snapshot, err = s.Zabbix.Collector.ZabbixProblems(query, d.Endpoint, credential, zabbixGroupIDs(d.Groups), previous)
		if err == nil {
			err = s.bindZabbixClusters(query, d.Scope, &snapshot)
		}
		cancel()
	}
	if ctx.Err() == nil {
		if err := s.Store.finishZabbix(ctx, d, snapshot, err); err != nil && !errors.Is(err, ErrVersion) {
			slog.WarnContext(ctx, "diagnostic Zabbix snapshot could not be saved", "sourceId", d.ID)
		}
	}
}

func (s *Server) bindZabbixClusters(ctx context.Context, scope ZabbixScope, snapshot *assetmeta.ZabbixSnapshot) error {
	checked := map[string]bool{}
	hostClusters := map[string]string{}
	snapshot.Hosts = slices.Clone(snapshot.Hosts)
	snapshot.Problems = slices.Clone(snapshot.Problems)
	for i, host := range snapshot.Hosts {
		for _, tag := range host.Tags {
			if tag.Tag != "kube-aiops.cluster-id" || !validID(tag.Value) || s.Zabbix.ClusterBinding == nil {
				continue
			}
			ok, known := checked[tag.Value]
			if !known {
				var err error
				ok, err = s.Zabbix.ClusterBinding(ctx, scope, tag.Value)
				if err != nil {
					return err
				}
				checked[tag.Value] = ok
			}
			if ok {
				if other := hostClusters[host.ID]; other != "" && other != tag.Value {
					delete(hostClusters, host.ID)
					break
				}
				hostClusters[host.ID] = tag.Value
			}
		}
		snapshot.Hosts[i].Tags = nil
		snapshot.Hosts[i].ClusterID = hostClusters[host.ID]
	}
	for i := range snapshot.Problems {
		snapshot.Problems[i].Hosts = slices.Clone(snapshot.Problems[i].Hosts)
		for j, host := range snapshot.Problems[i].Hosts {
			snapshot.Problems[i].Hosts[j].ClusterID = hostClusters[host.ID]
		}
	}
	return nil
}
