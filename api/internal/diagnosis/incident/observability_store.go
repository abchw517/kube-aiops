package incident

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/observability"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const obsColumns = "id,spec,credential,enabled,version,checked,synced,coverage_from,failed,snapshot"

func scanObservability(row rowScanner) (observabilityRecord, error) {
	var r observabilityRecord
	var doc []byte
	var id string
	var enabled, failed bool
	var version, checked, synced, coverage int64
	if err := row.Scan(&id, &doc, &r.encrypted, &enabled, &version, &checked, &synced, &coverage, &failed, &r.snapshot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	if err := json.Unmarshal(doc, &r.ObservabilitySource); err != nil {
		return r, err
	}
	r.ID, r.Enabled, r.Version, r.CheckedAt, r.SyncedAt, r.Failed = id, enabled, version, checked, synced, failed
	r.CoverageFrom = coverage
	if r.Kind == "alertmanager" && synced > 0 && time.Now().Unix()-synced > 180 {
		r.Failed = true
	}
	return r, nil
}

func (s *Store) obsByID(ctx context.Context, id string) (observabilityRecord, error) {
	return scanObservability(s.db.QueryRowContext(ctx, "SELECT "+obsColumns+" FROM diagnostic_observability WHERE id=?", id))
}

func (s *Store) obsRows(ctx context.Context, where string, args ...any) ([]observabilityRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+obsColumns+" FROM diagnostic_observability "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []observabilityRecord{}
	for rows.Next() {
		row, err := scanObservability(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Server) listObservability(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	all, err := s.Store.obsRows(ctx, "ORDER BY kind,id")
	if err != nil {
		return err
	}
	items := []ObservabilitySource{}
	for _, item := range all {
		if err := s.obsScope(ctx, p, ConnectorRead, item.Scope); err == nil {
			items = append(items, item.ObservabilitySource)
		} else if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrBadScope) {
			return err
		}
	}
	legacy := []string{}
	if s.require(ctx, p, ConnectorRead, Scope{}, true) == nil {
		legacy = append(legacy, s.Observability.Legacy...)
	}
	adminhttp.WriteJSON(w, map[string]any{"items": items, "legacy": legacy})
	return nil
}

func (s *Server) saveObservability(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	var input ObservabilityInput
	if !adminhttp.Decode(w, r, &input) {
		return obsInvalid
	}
	id := r.PathValue("sourceId")
	create := id == ""
	var version int64
	enabled := true
	if create {
		if input.SourceID != "" {
			return obsInvalid
		}
		id = zabbixID()
	} else {
		old, err := s.Store.obsByID(ctx, id)
		if err != nil {
			return err
		}
		if err = s.obsScope(ctx, p, ConnectorWrite, old.Scope); err != nil {
			return err
		}
		if old.Kind != input.Kind || old.Scope != input.Scope {
			return obsInvalid
		}
		enabled = old.Enabled
		v, err := requestedVersion(r)
		if err != nil {
			return err
		}
		version = v
		if version != old.Version || input.SourceID != "" && input.SourceID != id {
			return ErrVersion
		}
	}
	if err := s.obsScope(ctx, p, ConnectorWrite, input.Scope); err != nil {
		return err
	}
	d, c, err := s.obsInput(r, p, input)
	if err != nil {
		return err
	}
	preview, err := s.readObservability(ctx, d, c, input.DashboardIDs, true, false)
	if err != nil {
		return observabilityFailure(err)
	}
	if err = s.obsScope(ctx, p, ConnectorWrite, input.Scope); err != nil {
		return err
	}
	d.Dashboards = preview.Dashboards
	d.ID, d.Enabled, d.CheckedAt = id, enabled, time.Now().Unix()
	encrypted, err := s.Observability.Seal(ctx, id, d.Endpoint, c)
	if err != nil {
		return err
	}
	doc, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tx, err := s.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if create {
		result, err = tx.ExecContext(ctx, `INSERT INTO diagnostic_observability(id,kind,environment,business,cluster,spec,credential,checked)
    VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(kind,environment,business,cluster) DO NOTHING`, id, d.Kind, d.Scope.Environment, d.Scope.Business, d.Scope.Cluster, doc, encrypted, d.CheckedAt)
		d.Version = 1
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE diagnostic_observability SET spec=?,credential=?,checked=?,synced=0,coverage_from=0,due=0,failed=0,snapshot='[]',
    version=version+1 WHERE id=? AND version=?`, doc, encrypted, d.CheckedAt, id, version)
		d.Version = version + 1
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		if create {
			return ErrConflict
		}
		return ErrVersion
	}
	if s.Store.audit != nil {
		if err = s.Store.audit(ctx, tx, p.Subject, "observability.save", id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	adminhttp.WriteJSON(w, d)
	return nil
}

func (s *Server) toggleObservability(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	var body struct{ Enabled *bool }
	if !adminhttp.Decode(w, r, &body) || body.Enabled == nil {
		return obsInvalid
	}
	d, err := s.Store.obsByID(r.Context(), r.PathValue("sourceId"))
	if err != nil {
		return err
	}
	if err = s.obsScope(r.Context(), p, ConnectorWrite, d.Scope); err != nil {
		return err
	}
	v, err := requestedVersion(r)
	if err != nil {
		return err
	}
	if *body.Enabled && !s.obsAllowed(d.ObservabilitySource) {
		return obsEndpoint
	}
	if err = s.mutateObservability(r.Context(), p, d.ID, v, "observability.toggle",
		"UPDATE diagnostic_observability SET enabled=?,version=version+1,due=0 WHERE id=? AND version=?", *body.Enabled, d.ID, v); err != nil {
		return err
	}
	adminhttp.WriteJSON(w, map[string]bool{"enabled": *body.Enabled})
	return nil
}

func (s *Server) queueObservability(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	d, err := s.Store.obsByID(ctx, r.PathValue("sourceId"))
	if err != nil {
		return err
	}
	if err = s.obsScope(ctx, p, ConnectorTest, d.Scope); err != nil {
		return err
	}
	if !d.Enabled || !s.obsAllowed(d.ObservabilitySource) {
		return obsInvalid
	}
	if d.Kind != "alertmanager" {
		credential, err := s.Observability.Open(d.ID, d.Endpoint, d.encrypted)
		if err != nil {
			return err
		}
		if _, err = s.readObservability(ctx, d.ObservabilitySource, credential, nil, false, false); err != nil {
			return observabilityFailure(err)
		}
		err = s.mutateObservability(ctx, p, d.ID, d.Version, "observability.recheck",
			"UPDATE diagnostic_observability SET checked=? WHERE id=? AND version=?", time.Now().Unix(), d.ID, d.Version)
		if err != nil {
			return err
		}
	} else {
		err = s.mutateObservability(ctx, p, d.ID, d.Version, "observability.sync",
			"UPDATE diagnostic_observability SET due=0, version=version+1 WHERE id=? AND version=?", d.ID, d.Version)
		if err != nil {
			return err
		}
	}
	adminhttp.WriteJSON(w, map[string]bool{"queued": d.Kind == "alertmanager"})
	return nil
}

func (s *Server) mutateObservability(ctx context.Context, p identity.Principal, id string, version int64, action, query string, args ...any) error {
	tx, err := s.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrVersion
	}
	if s.Store.audit != nil {
		if err = s.Store.audit(ctx, tx, p.Subject, action, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) runObservability(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for range 8 {
			if !s.pollObservability(ctx) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) pollObservability(ctx context.Context) bool {
	now := time.Now().Unix()
	d, err := scanObservability(s.Store.db.QueryRowContext(ctx, `UPDATE diagnostic_observability SET due=? WHERE id=(
 SELECT id FROM diagnostic_observability WHERE kind='alertmanager' AND enabled=1 AND due<=? ORDER BY due,id LIMIT 1)
 RETURNING `+obsColumns, now+60, now))
	if err != nil {
		return false
	}
	scope := ZabbixScope{Environment: d.Scope.Environment, Business: d.Scope.Business}
	active, err := s.Zabbix.ScopeActive(ctx, scope)
	if err == nil && active {
		active, err = s.Zabbix.ClusterBinding(ctx, scope, d.Scope.Cluster)
	}
	var result ObservabilityPreview
	if err == nil && active && s.obsAllowed(d.ObservabilitySource) {
		var c observability.Credential
		c, err = s.Observability.Open(d.ID, d.Endpoint, d.encrypted)
		if err == nil {
			result, err = s.readObservability(ctx, d.ObservabilitySource, c, nil, true, true)
		}
	} else {
		err = obsUpstream
	}
	if err != nil {
		_, _ = s.Store.db.ExecContext(ctx, "UPDATE diagnostic_observability SET checked=?,failed=1 WHERE id=? AND version=? AND due=?", now, d.ID, d.Version, now+60)
		return true
	}
	merged := retainObservabilityAlerts(d.snapshot, result.Alerts, now)
	body, err := json.Marshal(merged)
	if err == nil {
		_, _ = s.Store.db.ExecContext(ctx, `UPDATE diagnostic_observability SET checked=?,synced=?,coverage_from=CASE WHEN coverage_from=0 THEN ? ELSE coverage_from END,failed=0,snapshot=?
    WHERE id=? AND version=? AND due=?`, now, now, now, body, d.ID, d.Version, now+60)
	}
	return true
}

func retainObservabilityAlerts(snapshot []byte, current []ObservabilityAlert, now int64) []ObservabilityAlert {
	active := make(map[string]bool, len(current))
	out := append([]ObservabilityAlert{}, current...)
	for _, row := range current {
		active[row.ID] = true
	}
	var old []ObservabilityAlert
	_ = json.Unmarshal(snapshot, &old)
	for _, row := range old {
		start, err := time.Parse(time.RFC3339Nano, row.StartedAt)
		if active[row.ID] || err != nil || start.Unix() < now-30*86400 || len(out) >= 1000 {
			continue
		}
		row.State = "departed"
		out = append(out, row)
	}
	return out
}

func (s *Server) listObservabilityAlerts(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	query := r.URL.Query()
	rows, err := s.Store.obsRows(ctx, "WHERE kind='alertmanager' AND enabled=1 ORDER BY id")
	if err != nil {
		return err
	}
	items := []ObservabilityAlert{}
	sources := []ObservabilitySource{}
	seen := map[string]bool{}
	truncated := false
	for _, row := range rows {
		d := row.Scope
		if (query.Get("environmentId") != "" && query.Get("environmentId") != d.Environment) ||
			(query.Get("businessId") != "" && query.Get("businessId") != d.Business) ||
			(query.Get("clusterId") != "" && query.Get("clusterId") != d.Cluster) {
			continue
		}
		if s.obsScope(ctx, p, ConnectorRead, d) != nil || !s.obsAllowed(row.ObservabilitySource) {
			continue
		}
		sources = append(sources, row.ObservabilitySource)
		var alerts []ObservabilityAlert
		if json.Unmarshal(row.snapshot, &alerts) != nil {
			continue
		}
		for _, alert := range alerts {
			if alert.State != "active" && query.Get("recent") != "true" {
				continue
			}
			allowed, err := s.Observability.AlertNamespace(ctx, d, alert.Namespace)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			scopedID := d.Environment + "\x00" + d.Business + "\x00" + d.Cluster + "\x00" + alert.ID
			if seen[scopedID] {
				continue
			}
			if len(items) >= 2000 {
				truncated = true
				break
			}
			seen[scopedID] = true
			items = append(items, alert)
		}
	}
	adminhttp.WriteJSON(w, map[string]any{"items": items, "sources": sources, "truncated": truncated})
	return nil
}

func (s *Server) listObservabilityDashboards(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	q := r.URL.Query()
	scope, err := s.ResolveScope(ctx, Scope{ClusterID: q.Get("clusterId"), Namespace: q.Get("namespace"), BusinessID: q.Get("businessId")})
	if err != nil {
		return err
	}
	if err = s.require(ctx, p, EvidencePermission, scope, false); err != nil {
		return err
	}
	rows, err := s.Store.obsRows(ctx, "WHERE kind='grafana' AND enabled=1 AND environment=? AND business=? AND cluster=?",
		scope.EnvironmentID, scope.BusinessID, scope.ClusterID)
	if err != nil {
		return err
	}
	end := time.Now().UTC()
	if raw := q.Get("anchor"); raw != "" {
		end, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil || end.After(time.Now().Add(time.Minute)) {
			return obsInvalid
		}
	}
	params := url.Values{"from": {strconv.FormatInt(end.Add(-30*time.Minute).UnixMilli(), 10)}, "to": {strconv.FormatInt(end.UnixMilli(), 10)}}
	items := []ObservabilityDashboard{}
	for _, row := range rows {
		if !s.obsAllowed(row.ObservabilitySource) {
			continue
		}
		for _, dashboard := range row.Dashboards {
			dashboard.URL = row.Endpoint + "/d/" + url.PathEscape(dashboard.UID) + "?" + params.Encode()
			items = append(items, dashboard)
		}
	}
	adminhttp.WriteJSON(w, map[string]any{"items": items})
	return nil
}

// ManagedObservability returns only an enabled exact binding. A configured
// binding whose credential cannot be loaded must not fall back to another source.
func (s *Server) ManagedObservability(ctx context.Context, kind string, scope Scope) (observability.Config, bool, error) {
	rows, err := s.Store.obsRows(ctx, "WHERE kind=? AND enabled=1 AND environment=? AND business=? AND cluster=?",
		kind, scope.EnvironmentID, scope.BusinessID, scope.ClusterID)
	if err != nil || len(rows) == 0 {
		return observability.Config{}, false, err
	}
	d := rows[0]
	active, err := s.Zabbix.ClusterBinding(ctx, ZabbixScope{Environment: scope.EnvironmentID, Business: scope.BusinessID}, scope.ClusterID)
	if err != nil {
		return observability.Config{}, true, err
	}
	if !active || !s.obsAllowed(d.ObservabilitySource) {
		return observability.Config{}, true, obsEndpoint
	}
	c, err := s.Observability.Open(d.ID, d.Endpoint, d.encrypted)
	return obsConfig(d.ObservabilitySource, c), true, err
}
