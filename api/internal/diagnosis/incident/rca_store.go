package incident

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const rcaColumns = `id,incident_id,actor,observation,snapshots,state,version,created_at,started_at,finished_at,error_code,lease,policy_id,policy_version`

func readJob(row rowScanner) (rcaJob, error) {
	var j rcaJob
	var actor, snapshots []byte
	var created int64
	var started, finished sql.NullInt64
	err := row.Scan(&j.ID, &j.IncidentID, &actor, &j.Observation, &snapshots, &j.State, &j.Version, &created, &started, &finished,
		&j.ErrorCode, &j.Lease, &j.PolicyID, &j.PolicyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal(actor, &j.ActorContext); err != nil {
		return j, err
	}
	if err = json.Unmarshal(snapshots, &j.Snapshots); err != nil {
		return j, err
	}
	j.Actor = j.ActorContext.Subject
	j.CreatedAt = time.UnixMilli(created).UTC()
	j.StartedAt, j.FinishedAt = nullableTime(started), nullableTime(finished)
	j.Revisions = []RCARevision{}
	j.Evidence = make([]Evidence, 0, len(j.Snapshots))
	for _, v := range j.Snapshots {
		j.Evidence = append(j.Evidence, v.Evidence)
	}
	return j, nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	at := time.UnixMilli(value.Int64).UTC()
	return &at
}

func readRun(ctx context.Context, db queryer, incidentID, id string) (rcaJob, error) {
	return readJob(db.QueryRowContext(ctx, "SELECT "+rcaColumns+" FROM diagnostic_rca_run WHERE incident_id=? AND id=?", incidentID, id))
}

type queryRows interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func runRevisions(ctx context.Context, db queryRows, id string) ([]RCARevision, error) {
	rows, err := db.QueryContext(ctx, "SELECT version,actor,at,conclusion FROM diagnostic_rca_revision WHERE run_id=? ORDER BY version DESC LIMIT 50", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RCARevision{}
	for rows.Next() {
		var v RCARevision
		var at int64
		var raw []byte
		if err := rows.Scan(&v.Version, &v.Actor, &at, &raw); err != nil {
			return nil, err
		}
		v.At = time.UnixMilli(at).UTC()
		if err := json.Unmarshal(raw, &v.Conclusion); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func snapshot(ctx context.Context, db queryer, incidentID, id string, now time.Time) (rcaEvidence, error) {
	var v rcaEvidence
	var observed, collected int64
	err := db.QueryRowContext(ctx, `SELECT id,source,kind,state,observed_at,collected_at,summary,document
		FROM diagnostic_evidence WHERE incident_id=? AND id=?`, incidentID, id).
		Scan(&v.ID, &v.Source, &v.Kind, &v.State, &observed, &collected, &v.Summary, &v.Signal)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	v.ObservedAt, v.CollectedAt = time.UnixMilli(observed).UTC(), time.UnixMilli(collected).UTC()
	v.Historical = v.ObservedAt.After(now) || now.Sub(v.ObservedAt) > 30*time.Minute
	return v, err
}

func evidenceCurrent(ctx context.Context, db queryer, j rcaJob) error {
	for _, v := range j.Snapshots {
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM diagnostic_evidence WHERE incident_id=? AND id=?)",
			j.IncidentID, v.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Store) recordRun(ctx context.Context, tx *sql.Tx, incidentID, actor, kind, runID string) error {
	i, err := get(ctx, tx, incidentID)
	if err != nil {
		return err
	}
	i.UpdatedAt = nowUTC()
	return s.record(ctx, tx, i, actor, kind, "", "", runID)
}

type policyTrigger struct {
	PolicyID                string
	Version                 int64
	ResourceID, SignalID    string
	SignalAt                int64
	Cooldown, MaxConcurrent int
}

func (s *Store) queueRun(ctx context.Context, i Incident, who taskActor, key, digest string, input RCAInput, policy *policyTrigger) (RCATask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RCATask{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE diagnostic_incident SET version=version WHERE id=?", i.ID); err != nil {
		return RCATask{}, err
	}
	whoKey := who.Provider + "\x00" + who.Subject
	if policy != nil {
		whoKey += "\x00policy:" + policy.PolicyID
	}
	var previous, id string
	err = tx.QueryRowContext(ctx, "SELECT id,digest FROM diagnostic_rca_run WHERE incident_id=? AND requester=? AND request_key=?",
		i.ID, whoKey, key).Scan(&id, &previous)
	if err == nil {
		if previous != digest {
			return RCATask{}, ErrConflict
		}
		existing, err := readRun(ctx, tx, i.ID, id)
		if err == nil {
			err = evidenceCurrent(ctx, tx, existing)
		}
		return existing.RCATask, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RCATask{}, err
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM diagnostic_rca_run WHERE incident_id=? AND state IN ('queued','running'))", i.ID).Scan(&busy); err != nil {
		return RCATask{}, err
	}
	if busy {
		return RCATask{}, ErrConflict
	}
	now := nowUTC()
	policyID := ""
	var policyVersion int64
	if policy != nil {
		if err = validateTrigger(ctx, tx, i, *policy, now); err != nil {
			return RCATask{}, err
		}
		policyID, policyVersion = policy.PolicyID, policy.Version
	}
	evidence := []rcaEvidence{}
	for _, evidenceID := range input.EvidenceIDs {
		v, err := snapshot(ctx, tx, i.ID, evidenceID, now)
		if err != nil {
			return RCATask{}, err
		}
		evidence = append(evidence, v)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return RCATask{}, err
	}
	actor, err := json.Marshal(who)
	if err != nil {
		return RCATask{}, err
	}
	id = rand.Text()
	_, err = tx.ExecContext(ctx, `INSERT INTO diagnostic_rca_run
		(id,incident_id,requester,request_key,digest,actor,observation,snapshots,state,created_at,policy_id,policy_version)
		VALUES(?,?,?,?,?,?,?,?,'queued',?,?,?)`, id, i.ID, whoKey, key, digest, actor, input.Observation, raw, now.UnixMilli(), policyID, policyVersion)
	if err != nil {
		return RCATask{}, err
	}
	if policy != nil {
		_, err = tx.ExecContext(ctx, "INSERT INTO diagnostic_policy_execution VALUES(?,?,?,?,?,?,?,?)", policyID, policyVersion,
			policy.ResourceID, i.ID, policy.SignalID, policy.SignalAt, id, now.UnixMilli())
		if err != nil {
			return RCATask{}, err
		}
	}
	if err = s.recordRun(ctx, tx, i.ID, who.Subject, "rca_queued", id); err != nil {
		return RCATask{}, err
	}
	j, err := readRun(ctx, tx, i.ID, id)
	if err != nil {
		return RCATask{}, err
	}
	return j.RCATask, tx.Commit()
}

func (s *Store) claimRun(ctx context.Context) (rcaJob, error) {
	now := nowUTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE diagnostic_rca_run SET state='timed_out',error_code='lease_expired',finished_at=?,version=version+1
		WHERE state='running' AND lease_until<=?`, now.UnixMilli(), now.UnixMilli()); err != nil {
		return rcaJob{}, err
	}
	return readJob(s.db.QueryRowContext(ctx, `UPDATE diagnostic_rca_run SET state='running',started_at=?,lease=?,lease_until=?,version=version+1
		WHERE id=(SELECT id FROM diagnostic_rca_run WHERE state='queued' ORDER BY created_at,id LIMIT 1) AND state='queued'
		RETURNING `+rcaColumns, now.UnixMilli(), rand.Text(), now.Add(3*time.Minute).UnixMilli()))
}

func insertConclusion(ctx context.Context, tx *sql.Tx, id, actor string, version int64, result RCAConclusion, now time.Time) error {
	value, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO diagnostic_rca_revision VALUES(?,?,?,?,?)", id, version, actor, now.UnixMilli(), value)
	return err
}

func (s *Store) finishRun(ctx context.Context, j rcaJob, state, code string, result *RCAConclusion) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowUTC()
	var v int64
	err = tx.QueryRowContext(ctx, `UPDATE diagnostic_rca_run SET state=?,error_code=?,finished_at=?,version=version+1
		WHERE id=? AND state='running' AND lease=? AND lease_until>? RETURNING version`, state, code, now.UnixMilli(), j.ID, j.Lease, now.UnixMilli()).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if result != nil {
		if err = evidenceCurrent(ctx, tx, j); err != nil {
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE diagnostic_rca_run SET state='failed',error_code='evidence_changed' WHERE id=?", j.ID); err != nil {
				return err
			}
		} else if err = insertConclusion(ctx, tx, j.ID, "模型", v, *result, now); err != nil {
			return err
		}
	}
	if err = s.recordRun(ctx, tx, j.IncidentID, j.Actor, "rca_completed", j.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) cancelRun(ctx context.Context, j rcaJob, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	out, err := tx.ExecContext(ctx, `UPDATE diagnostic_rca_run SET state='canceled',finished_at=?,version=version+1
		WHERE id=? AND state IN ('queued','running')`, nowUTC().UnixMilli(), j.ID)
	if err != nil {
		return err
	}
	n, err := out.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	if err = s.recordRun(ctx, tx, j.IncidentID, actor, "rca_canceled", j.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) amendRun(ctx context.Context, j rcaJob, actor string, version int64, result RCAConclusion) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	out, err := tx.ExecContext(ctx, `UPDATE diagnostic_rca_run SET version=version+1
		WHERE id=? AND state='succeeded' AND version=?`, j.ID, version)
	if err != nil {
		return err
	}
	n, err := out.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrVersion
	}
	if err = evidenceCurrent(ctx, tx, j); err != nil {
		return err
	}
	if err = insertConclusion(ctx, tx, j.ID, actor, version+1, result, nowUTC()); err != nil {
		return err
	}
	if err = s.recordRun(ctx, tx, j.IncidentID, actor, "rca_saved", j.ID); err != nil {
		return err
	}
	return tx.Commit()
}
