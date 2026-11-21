package incident

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type signalMatch struct {
	id, incidentID, resourceID, evidenceID string
	at                                     int64
}

func (s *Server) runPolicies(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.evaluatePolicies(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "diagnostic policy evaluation unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) evaluatePolicies(ctx context.Context) error {
	if s.AutomationEnabled == nil {
		return nil
	}
	return scanConfigPages(ctx, s.Store.db, "diagnostic_policy", policyColumns, "", readPolicy, func(item storedPolicy) (bool, error) {
		if !item.Enabled || !s.autoEnabled(item.Scope) {
			return true, nil
		}
		return true, s.triggerPolicy(ctx, item)
	})
}

func (s *Server) triggerPolicy(ctx context.Context, item storedPolicy) error {
	p := item.owner.principal()
	if s.ValidateActor != nil {
		if err := s.ValidateActor(ctx, p); err != nil {
			return nil
		}
	}
	for _, code := range []string{PolicyWrite, ReadPermission, RCARun, EvidencePermission} {
		if err := s.require(ctx, p, code, item.Scope, false); err != nil {
			if errors.Is(err, ErrForbidden) {
				return nil
			}
			return err
		}
	}
	matches, err := s.Store.matchPolicy(ctx, item)
	if err != nil {
		return err
	}
	for _, m := range matches {
		i, err := s.Store.Get(ctx, m.incidentID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if s.ModelAvailable != nil && !s.ModelAvailable(i) {
			continue
		}
		t := &policyTrigger{PolicyID: item.ID, Version: item.Version, ResourceID: m.resourceID, SignalID: m.id, SignalAt: m.at}
		key := fmt.Sprintf("policy-%s-%d-%s-%d", item.ID, item.Version, m.id, m.at)
		_, err = s.Store.queueRun(ctx, i, item.owner, key, key, RCAInput{Observation: "策略触发 · " + item.Name, EvidenceIDs: []string{m.evidenceID}}, t)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) matchPolicy(ctx context.Context, p storedPolicy) ([]signalMatch, error) {
	// Match the latest version of each signal and keep completed observations out of the queue.
	rows, err := s.db.QueryContext(ctx, `SELECT x.id,x.incident_id,x.resource_id,x.last_seen,
		(SELECT v.id FROM diagnostic_evidence v WHERE v.signal_id=x.id AND v.incident_id=x.incident_id AND v.observed_at=x.last_seen
		 ORDER BY v.collected_at DESC,v.id DESC LIMIT 1)
		FROM diagnostic_signal x JOIN diagnostic_incident i ON i.id=x.incident_id
		WHERE i.environment=? AND i.business=? AND i.cluster=? AND i.namespace=? AND i.status IN ('pending','investigating')
		AND (i.severity=? OR ?='') AND (x.source=? OR ?='any') AND x.collected_at>=? AND x.state IN ('firing','observed')
		AND NOT EXISTS(SELECT 1 FROM diagnostic_policy_execution e WHERE e.policy_id=? AND e.policy_version=? AND e.signal_id=x.id AND e.signal_at=x.last_seen)
		AND NOT EXISTS(SELECT 1 FROM diagnostic_policy_execution e WHERE e.policy_id=? AND e.resource_id=x.resource_id AND e.queued_at>?)
		AND NOT EXISTS(SELECT 1 FROM diagnostic_rca_run j WHERE j.incident_id=i.id AND j.state IN ('queued','running'))
		ORDER BY x.collected_at DESC,x.id LIMIT 20`, p.Scope.EnvironmentID, p.Scope.BusinessID, p.Scope.ClusterID, p.Scope.Namespace,
		p.Severity, p.Severity, p.Source, p.Source, p.EffectiveAt.UnixMilli(), p.ID, p.Version, p.ID,
		nowUTC().Add(-time.Duration(p.CooldownSeconds)*time.Second).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []signalMatch{}
	for rows.Next() {
		var m signalMatch
		if err := rows.Scan(&m.id, &m.incidentID, &m.resourceID, &m.at, &m.evidenceID); err != nil {
			return nil, err
		}
		values = append(values, m)
	}
	return values, rows.Err()
}
