package incident

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/holmes"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func (s *Server) healthEvidence(ctx context.Context, p identity.Principal, source, sourceID, id string) (holmes.Scope, any, error) {
	scope := holmes.Scope{Source: source, SourceID: sourceID}
	if !validID(sourceID) || !validID(id) || s.Zabbix == nil {
		return scope, nil, ErrNotFound
	}
	switch source {
	case "zabbix":
		if err := s.require(ctx, p, ConnectorRead, Scope{}, true); err != nil {
			return scope, nil, err
		}
		d, err := s.sourceZabbix(ctx, p, sourceID, ConnectorRead)
		if err != nil {
			return scope, nil, err
		}
		if !d.Enabled || d.snapshot == nil {
			return scope, nil, ErrNotFound
		}
		if err = s.requireZabbix(ctx, p, "diagnosis.health.investigate", d.Scope); err != nil {
			return scope, nil, err
		}
		scope.Environment, scope.Business = d.Scope.Environment, d.Scope.Business
		for _, row := range projectZabbix(d, d.snapshot.Problems, true) {
			if row.ID == id {
				row.OriginalURL = ""
				return scope, row, nil
			}
		}
	case "alertmanager":
		if s.Observability == nil {
			return scope, nil, ErrNotFound
		}
		d, err := s.Store.obsByID(ctx, sourceID)
		if err != nil {
			return scope, nil, err
		}
		if !d.Enabled || d.Kind != "alertmanager" || !s.obsAllowed(d.ObservabilitySource) {
			return scope, nil, ErrNotFound
		}
		if err = s.obsScope(ctx, p, ConnectorRead, d.Scope); err != nil {
			return scope, nil, err
		}
		var rows []ObservabilityAlert
		if json.Unmarshal(d.snapshot, &rows) != nil {
			return scope, nil, upstream
		}
		for _, row := range rows {
			if row.ID != id {
				continue
			}
			ok, err := s.Observability.AlertNamespace(ctx, d.Scope, row.Namespace)
			if err != nil {
				return scope, nil, err
			}
			if !ok {
				return scope, nil, ErrNotFound
			}
			target := Scope{EnvironmentID: d.Scope.Environment, BusinessID: d.Scope.Business, ClusterID: d.Scope.Cluster, Namespace: row.Namespace}
			if err = s.require(ctx, p, "diagnosis.health.investigate", target, false); err != nil {
				return scope, nil, err
			}
			scope.Environment, scope.Business, scope.Cluster, scope.Namespace = d.Scope.Environment, d.Scope.Business, d.Scope.Cluster, row.Namespace
			row.Scope = d.Scope
			return scope, row, nil
		}
	}
	return scope, nil, ErrNotFound
}

func (s *Server) investigateHealth(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	source, sourceID, id := r.PathValue("source"), r.PathValue("sourceId"), r.PathValue("id")
	scope, evidence, err := s.healthEvidence(ctx, p, source, sourceID, id)
	if err != nil {
		return err
	}
	ctx, skills := holmes.TraceSkills(ctx)
	analysis, err := holmes.Investigate(ctx, scope, evidence)
	slog.InfoContext(ctx, "health alert investigation", "actor", p.Subject, "source", source, "source_id", sourceID, "record_id", id, "engine_success", err == nil)
	if errors.Is(err, holmes.ErrUnconfigured) {
		return httpError{503, "HOLMES_SCOPE_UNCONFIGURED", "当前数据范围尚未配置只读 Holmes 服务。"}
	}
	if err != nil {
		return httpError{503, "HOLMES_UNAVAILABLE", "调查服务暂不可用，请稍后重试。"}
	}
	if s.ValidateActor != nil {
		if err = s.ValidateActor(ctx, p); err != nil {
			return ErrForbidden
		}
	}
	current, _, err := s.healthEvidence(ctx, p, source, sourceID, id)
	if err != nil {
		return err
	}
	if current != scope {
		return ErrConflict
	}
	adminhttp.WriteJSON(w, struct {
		Analysis string   `json:"analysis"`
		Skills   []string `json:"appliedSkills,omitempty"`
	}{cleanText(analysis, 64<<10), *skills})
	return nil
}

// ValidateHolmesAlertScope checks the current source record before an administrator binds it.
func (s *Server) ValidateHolmesAlertScope(ctx context.Context, p identity.Principal, scope holmes.Scope) error {
	switch scope.Source {
	case "zabbix":
		if s.Zabbix == nil {
			return ErrNotFound
		}
		d, err := s.sourceZabbix(ctx, p, scope.SourceID, ConnectorRead)
		if err != nil {
			return err
		}
		if d.Enabled && d.Scope.Environment == scope.Environment && d.Scope.Business == scope.Business {
			return nil
		}
	case "alertmanager":
		if s.Observability == nil {
			return ErrNotFound
		}
		d, err := s.Store.obsByID(ctx, scope.SourceID)
		if err != nil {
			return err
		}
		if !d.Enabled || d.Kind != "alertmanager" || !s.obsAllowed(d.ObservabilitySource) ||
			d.Scope.Environment != scope.Environment || d.Scope.Business != scope.Business || d.Scope.Cluster != scope.Cluster {
			return ErrBadScope
		}
		if err := s.obsScope(ctx, p, ConnectorRead, d.Scope); err != nil {
			return err
		}
		ok, err := s.Observability.AlertNamespace(ctx, d.Scope, scope.Namespace)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return ErrBadScope
}
