package incident

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/observability"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const observabilitySchema = `
CREATE TABLE IF NOT EXISTS diagnostic_observability (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, environment TEXT NOT NULL, business TEXT NOT NULL, cluster TEXT NOT NULL,
 spec BLOB NOT NULL, credential BLOB NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, version INTEGER NOT NULL DEFAULT 1,
 checked INTEGER NOT NULL DEFAULT 0, synced INTEGER NOT NULL DEFAULT 0, coverage_from INTEGER NOT NULL DEFAULT 0, due INTEGER NOT NULL DEFAULT 0,
 failed INTEGER NOT NULL DEFAULT 0, snapshot BLOB NOT NULL DEFAULT '[]', UNIQUE(kind,environment,business,cluster)
);
CREATE INDEX IF NOT EXISTS diagnostic_observability_due ON diagnostic_observability(kind,enabled,due);
`

type ObservabilityIntegration struct {
	AlertNamespace func(context.Context, ObservabilityScope, string) (bool, error)
	Allowed        func(kind, endpoint string) bool
	Seal           func(context.Context, string, string, observability.Credential) ([]byte, error)
	Open           func(string, string, []byte) (observability.Credential, error)
	Legacy         []string
}

type ObservabilityScope struct {
	Environment string `json:"environmentId"`
	Business    string `json:"businessId"`
	Cluster     string `json:"clusterId"`
}

type ObservabilityDashboard struct {
	UID   string `json:"uid"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
}

type ObservabilityAlert struct {
	SourceID    string             `json:"sourceId,omitempty"`
	Namespace   string             `json:"namespace,omitempty"`
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	State       string             `json:"state"`
	Severity    string             `json:"severity"`
	RawSeverity string             `json:"rawSeverity,omitempty"`
	StartedAt   string             `json:"startedAt"`
	Scope       ObservabilityScope `json:"scope"`
}

type ObservabilitySource struct {
	CoverageFrom      int64                    `json:"coverageFrom,omitempty"`
	ID                string                   `json:"id"`
	Kind              string                   `json:"kind"`
	Name              string                   `json:"name"`
	Endpoint          string                   `json:"endpoint"`
	Scope             ObservabilityScope       `json:"scope"`
	Shared            bool                     `json:"shared"`
	Label             string                   `json:"label,omitempty"`
	Value             string                   `json:"value,omitempty"`
	Tenant            *observability.Tenant    `json:"tenant,omitempty"`
	IncludeSuppressed bool                     `json:"includeSuppressed,omitempty"`
	Dashboards        []ObservabilityDashboard `json:"dashboards,omitempty"`
	Auth              string                   `json:"auth"`
	Enabled           bool                     `json:"enabled"`
	Version           int64                    `json:"version"`
	CheckedAt         int64                    `json:"checkedAt,omitempty"`
	SyncedAt          int64                    `json:"syncedAt,omitempty"`
	Failed            bool                     `json:"failed,omitempty"`
}

type ObservabilityInput struct {
	Kind              string                    `json:"kind"`
	Name              string                    `json:"name"`
	Endpoint          string                    `json:"endpoint"`
	Scope             ObservabilityScope        `json:"scope"`
	Shared            bool                      `json:"shared"`
	Label             string                    `json:"label,omitempty"`
	Value             string                    `json:"value,omitempty"`
	Tenant            *observability.Tenant     `json:"tenant,omitempty"`
	IncludeSuppressed bool                      `json:"includeSuppressed,omitempty"`
	DashboardIDs      []string                  `json:"dashboardIds,omitempty"`
	Credential        *observability.Credential `json:"credential,omitempty"`
	SourceID          string                    `json:"sourceId,omitempty"`
	Preview           bool                      `json:"preview,omitempty"`
}

type ObservabilityPreview struct {
	Count      int                      `json:"count"`
	Suppressed int                      `json:"suppressed,omitempty"`
	Dashboards []ObservabilityDashboard `json:"dashboards,omitempty"`
	Alerts     []ObservabilityAlert     `json:"alerts,omitempty"`
}

type observabilityRecord struct {
	ObservabilitySource
	encrypted []byte
	snapshot  []byte
}

var (
	obsName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,63}$`)
	obsUID       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	obsAlertName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]{0,127}$`)
	obsSeverity  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
	obsInvalid   = httpError{400, "OBSERVABILITY_INVALID", "请检查数据源、认证和采集范围。"}
	obsEndpoint  = httpError{400, "OBSERVABILITY_ENDPOINT", "服务地址必须是平台允许的 HTTPS 基地址。"}
	obsUpstream  = httpError{502, "OBSERVABILITY_UPSTREAM", "上游连接失败或返回无法验证的数据，请检查网络、证书、凭据与范围。"}
)

func (s *Server) registerObservability(mux *http.ServeMux) {
	if s.Observability == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/diagnostics/observability/sources", s.protect(s.listObservability))
	mux.HandleFunc("POST /api/v1/diagnostics/observability/probe", s.protect(s.probeObservability))
	mux.HandleFunc("POST /api/v1/diagnostics/observability/sources", s.protect(s.saveObservability))
	mux.HandleFunc("PUT /api/v1/diagnostics/observability/sources/{sourceId}", s.protect(s.saveObservability))
	mux.HandleFunc("PATCH /api/v1/diagnostics/observability/sources/{sourceId}", s.protect(s.toggleObservability))
	mux.HandleFunc("DELETE /api/v1/diagnostics/observability/sources/{sourceId}", s.protect(s.deleteObservability))
	mux.HandleFunc("POST /api/v1/diagnostics/observability/sources/{sourceId}/sync", s.protect(s.queueObservability))
	mux.HandleFunc("GET /api/v1/diagnostics/observability/alerts", s.protect(s.listObservabilityAlerts))
	mux.HandleFunc("GET /api/v1/diagnostics/observability/dashboards", s.protect(s.listObservabilityDashboards))
}

func (s *Server) obsScope(ctx context.Context, p identity.Principal, code string, scope ObservabilityScope) error {
	z := ZabbixScope{Environment: scope.Environment, Business: scope.Business}
	if err := s.requireZabbix(ctx, p, code, z); err != nil {
		return err
	}
	ok, err := s.Zabbix.ClusterBinding(ctx, z, scope.Cluster)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadScope
	}
	return s.require(ctx, p, code, Scope{EnvironmentID: scope.Environment, BusinessID: scope.Business, ClusterID: scope.Cluster}, false)
}

func (s *Server) obsAllowed(d ObservabilitySource) bool {
	return s.Observability.Allowed(d.Kind, d.Endpoint)
}

func validObsCredential(kind string, c observability.Credential) bool {
	if kind == "grafana" && c.Method != "bearer" {
		return false
	}
	if len(c.Secret) > 8192 || strings.ContainsFunc(c.Secret, unicode.IsControl) {
		return false
	}
	switch c.Method {
	case "none":
		return c.Username == "" && c.Secret == ""
	case "bearer":
		return c.Username == "" && c.Secret != "" && !strings.ContainsFunc(c.Secret, func(r rune) bool { return r < '!' || r > '~' })
	case "basic":
		return c.Username != "" && len(c.Username) <= 256 && c.Secret != "" && !strings.Contains(c.Username, ":") && !strings.ContainsFunc(c.Username, unicode.IsControl)
	}
	return false
}

func (s *Server) obsInput(r *http.Request, p identity.Principal, input ObservabilityInput) (ObservabilitySource, observability.Credential, error) {
	ctx := r.Context()
	d := ObservabilitySource{Kind: input.Kind, Name: strings.TrimSpace(input.Name), Scope: input.Scope, Shared: input.Shared,
		Label: input.Label, Value: input.Value, Tenant: input.Tenant, IncludeSuppressed: input.IncludeSuppressed}
	var c observability.Credential
	if !slices.Contains([]string{"prometheus", "victorialogs", "alertmanager", "grafana"}, d.Kind) ||
		d.Name == "" || utf8.RuneCountInString(d.Name) > 120 || strings.ContainsFunc(d.Name, unicode.IsControl) ||
		len(input.DashboardIDs) > 25 || d.Kind != "grafana" && len(input.DashboardIDs) > 0 ||
		d.Kind != "victorialogs" && d.Tenant != nil || d.Kind != "alertmanager" && d.IncludeSuppressed {
		return d, c, obsInvalid
	}
	if d.Shared && (d.Kind == "grafana" || !obsName.MatchString(d.Label) || d.Kind != "victorialogs" && strings.Contains(d.Label, ".") ||
		len(d.Value) > 253 || strings.TrimSpace(d.Value) == "" || strings.ContainsFunc(d.Value, unicode.IsControl)) ||
		!d.Shared && (d.Label != "" || d.Value != "") {
		return d, c, obsInvalid
	}
	if err := s.obsScope(ctx, p, ConnectorTest, d.Scope); err != nil {
		return d, c, err
	}
	endpoint, err := observability.ManagedEndpoint(input.Endpoint)
	d.Endpoint = endpoint
	if err != nil || !s.obsAllowed(d) {
		return d, c, obsEndpoint
	}
	if input.Credential != nil {
		c = *input.Credential
	} else if input.SourceID != "" {
		old, err := s.Store.obsByID(ctx, input.SourceID)
		if err != nil {
			return d, c, err
		}
		if old.Kind != d.Kind || old.Endpoint != d.Endpoint || old.Scope != d.Scope {
			return d, c, obsInvalid
		}
		c, err = s.Observability.Open(old.ID, old.Endpoint, old.encrypted)
		if err != nil {
			return d, c, err
		}
	}
	if !validObsCredential(d.Kind, c) {
		return d, c, obsInvalid
	}
	d.Auth = c.Method
	return d, c, nil
}

func (s *Server) probeObservability(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	var input ObservabilityInput
	if !adminhttp.Decode(w, r, &input) {
		return obsInvalid
	}
	d, c, err := s.obsInput(r, p, input)
	if err != nil {
		return err
	}
	result, err := s.readObservability(r.Context(), d, c, input.DashboardIDs, input.Preview, false)
	if err != nil {
		return observabilityFailure(err)
	}
	adminhttp.WriteJSON(w, result)
	return nil
}

func observabilityFailure(err error) error {
	switch {
	case errors.Is(err, observability.ErrUpstreamPermission):
		return httpError{502, "OBSERVABILITY_UPSTREAM_FORBIDDEN", "上游拒绝读取，请核对只读凭据和授权范围。"}
	case errors.Is(err, observability.ErrUpstreamTimeout):
		return httpError{504, "OBSERVABILITY_UPSTREAM_TIMEOUT", "上游连接超时，请核对 API 服务的出站网络。"}
	case errors.Is(err, observability.ErrUpstreamTLS):
		return httpError{502, "OBSERVABILITY_UPSTREAM_TLS", "上游 HTTPS 证书验证失败，请核对证书和 API 信任链。"}
	case errors.Is(err, obsInvalid):
		return obsInvalid
	}
	return obsUpstream
}

func (s *Server) readObservability(ctx context.Context, d ObservabilitySource, c observability.Credential, selected []string, preview, full bool) (ObservabilityPreview, error) {
	safe := d
	if safe.Kind == "alertmanager" {
		safe.IncludeSuppressed = true
	}
	result, err := probeObservability(ctx, safe, c, selected, preview, safe.Kind == "alertmanager")
	if err != nil || d.Kind != "alertmanager" {
		return result, err
	}
	allowed := ObservabilityPreview{}
	for _, alert := range result.Alerts {
		yes, err := s.Observability.AlertNamespace(ctx, d.Scope, alert.Namespace)
		if err != nil {
			return ObservabilityPreview{}, err
		}
		if !yes {
			continue
		}
		if alert.State == "active" {
			allowed.Count++
		} else if d.IncludeSuppressed {
			allowed.Suppressed++
		} else {
			continue
		}
		if full || len(allowed.Alerts) < 5 {
			allowed.Alerts = append(allowed.Alerts, alert)
		}
	}
	return allowed, nil
}

func obsConfig(d ObservabilitySource, c observability.Credential) observability.Config {
	return observability.Config{URL: d.Endpoint, ClusterLabelKey: d.Label, ClusterLabelValue: d.Value, Tenant: d.Tenant, Credential: c, Managed: true}
}

func probeObservability(ctx context.Context, d ObservabilitySource, c observability.Credential, selected []string, preview, full bool) (ObservabilityPreview, error) {
	client, err := observability.NewClient("OBSERVABILITY", obsConfig(d, c))
	if err != nil {
		return ObservabilityPreview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := client.BearerToken()
	if err != nil {
		return ObservabilityPreview{}, err
	}
	params := url.Values{}
	var path string
	switch d.Kind {
	case "prometheus":
		path = "api/v1/query"
		query := "up"
		if d.Shared {
			query += "{" + d.Label + "=" + strconv.Quote(d.Value) + "}"
		}
		params.Set("query", query)
		params.Set("limit", "20")
		params.Set("timeout", "4s")
	case "victorialogs":
		path = "select/logsql/stats_query_range"
		query := "_time:15m"
		if d.Shared {
			query += " " + d.Label + ":=" + strconv.Quote(d.Value)
		}
		params.Set("query", query+" | stats count() as log_count")
		params.Set("start", time.Now().Add(-15*time.Minute).UTC().Format(time.RFC3339))
		params.Set("end", time.Now().UTC().Format(time.RFC3339))
		params.Set("step", "1m")
		params.Set("timeout", "4s")
	case "alertmanager":
		path = "api/v2/alerts"
		for _, filter := range []string{"active", "silenced", "inhibited", "unprocessed"} {
			params.Set(filter, "true")
		}
		if d.Shared {
			params.Add("filter", d.Label+"="+strconv.Quote(d.Value))
		}
	case "grafana":
		path = "api/search"
		params.Set("type", "dash-db")
		params.Set("limit", "100")
	}
	body, err := client.Get(ctx, path, params, token, 512<<10)
	if err != nil {
		return ObservabilityPreview{}, err
	}
	switch d.Kind {
	case "alertmanager":
		return readObservabilityAlerts(d, body, full)
	case "grafana":
		return readObservabilityDashboards(body, selected, preview)
	}
	return readObservabilitySeries(d.Kind, body)
}

func readObservabilitySeries(kind string, body []byte) (ObservabilityPreview, error) {
	var doc struct {
		Status   string
		Warnings []string
		Data     struct {
			ResultType string
			Result     []struct{ Values [][]json.RawMessage }
		}
	}
	if json.Unmarshal(body, &doc) != nil || doc.Status != "success" || len(doc.Warnings) > 0 || doc.Data.Result == nil {
		return ObservabilityPreview{}, obsUpstream
	}
	if kind == "prometheus" {
		if doc.Data.ResultType != "vector" {
			return ObservabilityPreview{}, obsUpstream
		}
		return ObservabilityPreview{Count: len(doc.Data.Result)}, nil
	}
	if doc.Data.ResultType != "matrix" {
		return ObservabilityPreview{}, obsUpstream
	}
	result := ObservabilityPreview{}
	for _, series := range doc.Data.Result {
		for _, point := range series.Values {
			if len(point) != 2 {
				return ObservabilityPreview{}, obsUpstream
			}
			n, err := strconv.ParseInt(strings.Trim(string(point[1]), `"`), 10, 53)
			if err != nil || n < 0 {
				return ObservabilityPreview{}, obsUpstream
			}
			result.Count += int(n)
		}
	}
	return result, nil
}

func readObservabilityDashboards(body []byte, selected []string, preview bool) (ObservabilityPreview, error) {
	var rows []ObservabilityDashboard
	if json.Unmarshal(body, &rows) != nil || rows == nil {
		return ObservabilityPreview{}, obsUpstream
	}
	if preview && len(selected) == 0 {
		return ObservabilityPreview{}, obsInvalid
	}
	items := make([]ObservabilityDashboard, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if obsUID.MatchString(row.UID) && !seen[row.UID] && (!preview || slices.Contains(selected, row.UID)) {
			seen[row.UID] = true
			items = append(items, ObservabilityDashboard{UID: row.UID, Title: row.Title})
		}
	}
	if preview && len(items) != len(selected) {
		return ObservabilityPreview{}, obsInvalid
	}
	return ObservabilityPreview{Count: len(items), Dashboards: items}, nil
}

func readObservabilityAlerts(d ObservabilitySource, body []byte, full bool) (ObservabilityPreview, error) {
	var rows []struct {
		Labels   map[string]string
		StartsAt time.Time
		Status   struct{ State string }
	}
	if json.Unmarshal(body, &rows) != nil || rows == nil || len(rows) > 500 {
		return ObservabilityPreview{}, obsUpstream
	}
	result := ObservabilityPreview{}
	seen := map[string]bool{}
	for _, row := range rows {
		if d.Shared && row.Labels[d.Label] != d.Value {
			continue
		}
		if row.StartsAt.IsZero() || !obsAlertName.MatchString(row.Labels["alertname"]) || !slices.Contains([]string{"active", "suppressed", "unprocessed"}, row.Status.State) {
			return ObservabilityPreview{}, obsUpstream
		}
		stable, _ := json.Marshal(row.Labels)
		start := row.StartsAt.UTC().Format(time.RFC3339Nano)
		digest := sha256.Sum256(append(stable, start...))
		id := hex.EncodeToString(digest[:])
		if seen[id] {
			continue
		}
		seen[id] = true
		raw := row.Labels["severity"]
		if !obsSeverity.MatchString(raw) {
			raw = ""
		}
		level := ""
		switch raw {
		case "critical", "warning", "info":
			level = raw
		}
		if row.Status.State == "active" {
			result.Count++
		} else {
			result.Suppressed++
		}
		if (row.Status.State == "active" || d.IncludeSuppressed) && (full || len(result.Alerts) < 5) {
			result.Alerts = append(result.Alerts, ObservabilityAlert{ID: id, Title: row.Labels["alertname"], State: row.Status.State, Severity: level,
				RawSeverity: raw, StartedAt: start, Scope: d.Scope, Namespace: row.Labels["namespace"]})
		}
	}
	if !d.IncludeSuppressed {
		result.Suppressed = 0
	}
	return result, nil
}
