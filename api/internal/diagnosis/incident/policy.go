package incident

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const (
	PolicyRead  = "diagnosis.policy.read"
	PolicyWrite = "diagnosis.policy.write"
)

type PolicyInput struct {
	Name            string `json:"name"`
	Scope           Scope  `json:"scope"`
	Enabled         bool   `json:"enabled"`
	Severity        string `json:"severity,omitempty"`
	Source          string `json:"source"`
	Template        string `json:"template"`
	CooldownSeconds int    `json:"cooldownSeconds"`
	MaxConcurrent   int    `json:"maxConcurrent"`
}

type Policy struct {
	PolicyInput
	ID                string    `json:"id"`
	Version           int64     `json:"version"`
	EffectiveAt       time.Time `json:"effectiveAt"`
	AutomationAllowed bool      `json:"automationAllowed"`
}

type PolicyExecution struct {
	IncidentID string    `json:"incidentId"`
	RunID      string    `json:"runId"`
	Version    int64     `json:"version"`
	State      string    `json:"state"`
	QueuedAt   time.Time `json:"queuedAt"`
}

type storedPolicy struct {
	Policy
	owner taskActor
}

const policyColumns = "id,scope,document,owner,version,enabled,effective_at"

func readPolicy(row rowScanner) (storedPolicy, error) {
	var out storedPolicy
	var scope, cfg, author []byte
	var at int64
	var enabled bool
	err := row.Scan(&out.ID, &scope, &cfg, &author, &out.Version, &enabled, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(cfg, &out.PolicyInput); err != nil {
		return out, err
	}
	if err = json.Unmarshal(scope, &out.Scope); err != nil {
		return out, err
	}
	if err = json.Unmarshal(author, &out.owner); err != nil {
		return out, err
	}
	out.Enabled = enabled
	out.EffectiveAt = time.UnixMilli(at).UTC()
	return out, nil
}

func policyByID(ctx context.Context, db queryer, id string) (storedPolicy, error) {
	return readPolicy(db.QueryRowContext(ctx, "SELECT "+policyColumns+" FROM diagnostic_policy WHERE id=?", id))
}

func (s *Server) registerPolicy(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/diagnostics/policies", s.protect(s.listPolicies))
	mux.HandleFunc("POST /api/v1/diagnostics/policies", s.protect(s.createPolicy))
	mux.HandleFunc("PATCH /api/v1/diagnostics/policies/{policyId}", s.protect(s.updatePolicy))
	mux.HandleFunc("GET /api/v1/diagnostics/policies/{policyId}/runs", s.protect(s.policyRuns))
}

func (s *Server) autoEnabled(scope Scope) bool {
	return s.AutomationEnabled != nil && s.AutomationEnabled(scope)
}

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	if err := s.require(ctx, p, PolicyRead, Scope{}, true); err != nil {
		return err
	}
	after, limit, err := requestPage(r, "policies")
	if err != nil {
		return err
	}
	page := Page[Policy]{Items: []Policy{}}
	err = scanConfigPages(ctx, s.Store.db, "diagnostic_policy", policyColumns, after.ID, readPolicy, func(item storedPolicy) (bool, error) {
		ok, err := s.Authorize(ctx, p, PolicyRead, item.Scope, false)
		if err != nil || !ok {
			return true, err
		}
		if len(page.Items) == limit {
			page.Pagination.Continue = encodeCursor(cursor{ID: page.Items[limit-1].ID, From: "policies"})
			return false, nil
		}
		item.AutomationAllowed = s.autoEnabled(item.Scope)
		page.Items = append(page.Items, item.Policy)
		return true, nil
	})
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, page)
	return nil
}

func validatePolicy(input *PolicyInput) bool {
	input.Name = cleanText(input.Name, 180)
	return input.Name != "" && input.Template == "evidence_rca" && (input.Severity == "" || validSeverity(input.Severity)) &&
		slices.Contains([]string{"any", "kubernetes-events", "prometheus", "victorialogs", "alertmanager", "zabbix", "aliyun-ecs", "tencent-cvm"}, input.Source) &&
		input.CooldownSeconds >= 60 && input.CooldownSeconds <= 86400 && input.MaxConcurrent >= 1 && input.MaxConcurrent <= 5
}

func (s *Server) createPolicy(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	var input PolicyInput
	if !adminhttp.Decode(w, r, &input) || !validatePolicy(&input) {
		return badInput
	}
	scope, err := s.canonicalScope(ctx, p, PolicyWrite, input.Scope)
	if err != nil {
		return err
	}
	input.Scope = scope
	return s.savePolicyResponse(w, r, p, input, "", 0)
}

func (s *Server) updatePolicy(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	item, err := policyByID(ctx, s.Store.db, r.PathValue("policyId"))
	if err != nil {
		return err
	}
	if err = s.require(ctx, p, PolicyWrite, item.Scope, false); err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	var input PolicyInput
	if !adminhttp.Decode(w, r, &input) || !validatePolicy(&input) || input.Scope != item.Scope {
		return badInput
	}
	return s.savePolicyResponse(w, r, p, input, item.ID, version)
}

func (s *Server) savePolicyResponse(w http.ResponseWriter, r *http.Request, p identity.Principal, input PolicyInput, id string, version int64) error {
	ctx := r.Context()
	if input.Enabled {
		for _, code := range []string{ReadPermission, RCARun, EvidencePermission} {
			if err := s.require(ctx, p, code, input.Scope, false); err != nil {
				return err
			}
		}
	}
	out, err := s.Store.savePolicy(ctx, input, actorFor(p), id, version)
	if err != nil {
		return err
	}
	out.AutomationAllowed = s.autoEnabled(out.Scope)
	setVersion(w, out.Version)
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
	}
	adminhttp.WriteJSON(w, out)
	return nil
}

func (s *Store) savePolicy(ctx context.Context, input PolicyInput, owner taskActor, id string, version int64) (Policy, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return Policy{}, err
	}
	who, err := json.Marshal(owner)
	if err != nil {
		return Policy{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Policy{}, err
	}
	defer tx.Rollback()
	now := nowUTC().UnixMilli()
	if id == "" {
		id = rand.Text()
		scope, _ := json.Marshal(input.Scope)
		_, err = tx.ExecContext(ctx, "INSERT INTO diagnostic_policy VALUES(?,?,?,?,1,?,?)", id, scope, data, who, input.Enabled, now)
		if err != nil {
			return Policy{}, err
		}
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE diagnostic_policy SET document=?,owner=?,version=version+1,enabled=?,effective_at=? WHERE id=? AND version=?`,
			data, who, input.Enabled, now, id, version)
		if err != nil {
			return Policy{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return Policy{}, err
		}
		if n == 0 {
			return Policy{}, ErrVersion
		}
		if _, err = tx.ExecContext(ctx, `UPDATE diagnostic_rca_run SET state='canceled',error_code='policy_changed',version=version+1,finished_at=?
			WHERE policy_id=? AND state='queued'`, now, id); err != nil {
			return Policy{}, err
		}
	}
	if s.audit != nil {
		if err = s.audit(ctx, tx, owner.Subject, "policy_saved", id); err != nil {
			return Policy{}, err
		}
	}
	out, err := policyByID(ctx, tx, id)
	if err != nil {
		return Policy{}, err
	}
	return out.Policy, tx.Commit()
}

func (s *Server) policyRuns(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	item, err := policyByID(ctx, s.Store.db, r.PathValue("policyId"))
	if err != nil {
		return err
	}
	if err = s.require(ctx, p, PolicyRead, item.Scope, false); err != nil {
		return err
	}
	for _, code := range []string{ReadPermission, RCARead} {
		if err = s.require(ctx, p, code, item.Scope, false); err != nil {
			return err
		}
	}
	page := []PolicyExecution{}
	rows, err := s.Store.db.QueryContext(ctx, `SELECT e.incident_id,e.run_id,e.policy_version,j.state,e.queued_at
		FROM diagnostic_policy_execution e JOIN diagnostic_rca_run j ON j.id=e.run_id
		WHERE e.policy_id=? ORDER BY e.queued_at DESC,e.run_id LIMIT 50`, item.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var result PolicyExecution
		var at int64
		if err = rows.Scan(&result.IncidentID, &result.RunID, &result.Version, &result.State, &at); err != nil {
			return err
		}
		result.QueuedAt = time.UnixMilli(at).UTC()
		page = append(page, result)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	adminhttp.WriteJSON(w, map[string]any{"items": page})
	return nil
}

func validateTrigger(ctx context.Context, tx *sql.Tx, i Incident, t policyTrigger, now time.Time) error {
	policy, err := policyByID(ctx, tx, t.PolicyID)
	if err != nil {
		return err
	}
	current, err := get(ctx, tx, i.ID)
	if err != nil {
		return err
	}
	if !policy.Enabled || policy.Version != t.Version || current.Scope != policy.Scope ||
		(current.Status != Pending && current.Status != Investigating) ||
		(policy.Severity != "" && current.Severity != policy.Severity) {
		return ErrConflict
	}
	var matches bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM diagnostic_signal WHERE id=? AND incident_id=? AND
		resource_id=? AND last_seen=? AND collected_at>=? AND state IN ('firing','observed') AND (source=? OR ?='any'))`,
		t.SignalID, i.ID, t.ResourceID, t.SignalAt, policy.EffectiveAt.UnixMilli(), policy.Source, policy.Source).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return ErrConflict
	}
	var active int
	err = tx.QueryRowContext(ctx, "SELECT count(*) FROM diagnostic_rca_run WHERE policy_id=? AND state IN ('queued','running')", t.PolicyID).Scan(&active)
	if err != nil {
		return err
	}
	if active >= policy.MaxConcurrent {
		return ErrConflict
	}
	var blocked bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM diagnostic_policy_execution WHERE policy_id=?
		AND ((policy_version=? AND signal_id=? AND signal_at=?) OR (resource_id=? AND queued_at>?)))`,
		t.PolicyID, t.Version, t.SignalID, t.SignalAt, t.ResourceID, now.Add(-time.Duration(policy.CooldownSeconds)*time.Second).UnixMilli()).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return ErrConflict
	}
	return nil
}

func (s *Store) policyJobCurrent(ctx context.Context, j rcaJob, i Incident) error {
	item, err := policyByID(ctx, s.db, j.PolicyID)
	if err != nil {
		return err
	}
	if !item.Enabled || item.Version != j.PolicyVersion || item.Scope != i.Scope ||
		i.Status != Pending && i.Status != Investigating {
		return ErrConflict
	}
	return nil
}
