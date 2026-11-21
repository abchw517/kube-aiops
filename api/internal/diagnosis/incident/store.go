package incident

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const columns = `number,id,environment,business,cluster,namespace,resource_id,title,description,resource,finding_id,
severity,status,owner_group,source,version,first_seen,last_seen,created_at,updated_at,anchor_at,merged_into,sources`

type rowScanner interface{ Scan(...any) error }
type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanIncident(row rowScanner) (Incident, error) {
	var out Incident
	var number, first, last, created, updated, anchor int64
	var resource, sources []byte
	err := row.Scan(&number, &out.ID, &out.Scope.EnvironmentID, &out.Scope.BusinessID, &out.Scope.ClusterID,
		&out.Scope.Namespace, &out.ResourceID, &out.Title, &out.Description, &resource, &out.FindingID,
		&out.Severity, &out.Status, &out.OwnerGroup, &out.Source, &out.Version, &first, &last, &created, &updated, &anchor,
		&out.MergedInto, &sources)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	if err := json.Unmarshal(resource, &out.Resource); err != nil {
		return Incident{}, err
	}
	if err := json.Unmarshal(sources, &out.Sources); err != nil {
		return Incident{}, err
	}
	out.FirstSeen, out.LastSeen = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
	out.CreatedAt, out.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	out.AnchorAt = time.UnixMilli(anchor).UTC()
	out.DisplayKey = displayKey(out.CreatedAt.Year(), number)
	return out, nil
}

func get(ctx context.Context, db queryer, id string) (Incident, error) {
	return scanIncident(db.QueryRowContext(ctx, "SELECT "+columns+" FROM diagnostic_incident WHERE id=?", id))
}

func (s *Store) Get(ctx context.Context, id string) (Incident, error) {
	return get(ctx, s.db, id)
}

func insert(ctx context.Context, tx *sql.Tx, i Incident) (Incident, error) {
	resource, err := json.Marshal(i.Resource)
	if err != nil {
		return Incident{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO diagnostic_incident
		(id,environment,business,cluster,namespace,resource_id,title,description,resource,finding_id,
		 severity,status,owner_group,source,version,first_seen,last_seen,created_at,updated_at,anchor_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?)`,
		i.ID, i.Scope.EnvironmentID, i.Scope.BusinessID, i.Scope.ClusterID, i.Scope.Namespace, i.ResourceID,
		i.Title, i.Description, resource, i.FindingID, i.Severity, i.Status, i.OwnerGroup, i.Source,
		i.FirstSeen.UnixMilli(), i.LastSeen.UnixMilli(), i.CreatedAt.UnixMilli(), i.UpdatedAt.UnixMilli(), i.AnchorAt.UnixMilli())
	if err != nil {
		return Incident{}, err
	}
	n, err := result.LastInsertId()
	i.DisplayKey = displayKey(i.CreatedAt.Year(), n)
	i.Version = 1
	return i, err
}

func (s *Store) Create(ctx context.Context, i Incident, actor, requester, key, digest string, anchor *assetIncidentAnchor) (Incident, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, false, err
	}
	defer tx.Rollback()
	i.ID = rand.Text()
	result, err := tx.ExecContext(ctx, `INSERT INTO diagnostic_incident_idempotency VALUES (?,?,?,?)
		ON CONFLICT(actor,key) DO NOTHING`, requester, key, digest, i.ID)
	if err != nil {
		return Incident{}, false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Incident{}, false, err
	}
	if count == 0 {
		existing, err := replay(ctx, tx, requester, key, digest)
		return existing, false, err
	}

	i, err = insert(ctx, tx, i)
	if err != nil {
		return Incident{}, false, err
	}
	if anchor != nil {
		var valid bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM diagnostic_asset a JOIN diagnostic_datasource d ON d.id=a.datasource_id
			WHERE a.id=? AND d.id=? AND d.enabled=1 AND d.version=?)`, anchor.ID, anchor.ConnectorID, anchor.Version).Scan(&valid)
		if err != nil {
			return Incident{}, false, err
		}
		if !valid {
			return Incident{}, false, ErrVersion
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO diagnostic_dependency VALUES(?,'asset',?,'monitored_by','confirmed',?,?,?,'')`,
			i.ID, anchor.ID, "从数据源资产创建故障", actor, i.CreatedAt.UnixMilli())
		if err != nil {
			return Incident{}, false, err
		}
	}
	if err = s.record(ctx, tx, i, actor, "created", "", "", i.Status); err != nil {
		return Incident{}, false, err
	}
	return i, true, tx.Commit()
}

func replay(ctx context.Context, db queryer, actor, key, digest string) (Incident, error) {
	var previous, id string
	err := db.QueryRowContext(ctx, "SELECT digest,incident_id FROM diagnostic_incident_idempotency WHERE actor=? AND key=?", actor, key).Scan(&previous, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	if previous != digest {
		return Incident{}, ErrConflict
	}
	return get(ctx, db, id)
}

func lockedIncident(ctx context.Context, tx *sql.Tx, id string, version int64) (Incident, error) {
	if _, err := tx.ExecContext(ctx, "UPDATE diagnostic_incident SET version=version WHERE id=?", id); err != nil {
		return Incident{}, err
	}
	i, err := get(ctx, tx, id)
	if err == nil && i.Version != version {
		return Incident{}, ErrVersion
	}
	return i, err
}

func (s *Store) record(ctx context.Context, tx *sql.Tx, i Incident, actor, kind, reason, before, after string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO diagnostic_timeline(incident_id,at,actor,kind,reason,before_state,after_state)
		VALUES (?,?,?,?,?,?,?)`, i.ID, i.UpdatedAt.UnixMilli(), actor, kind, reason, before, after)
	if err != nil || s.audit == nil {
		return err
	}
	return s.audit(ctx, tx, actor, kind, i.DisplayKey)
}

func persist(ctx context.Context, tx *sql.Tx, i *Incident) error {
	sources, err := json.Marshal(i.Sources)
	if err != nil {
		return err
	}
	i.Version++
	_, err = tx.ExecContext(ctx, `UPDATE diagnostic_incident SET status=?,severity=?,owner_group=?,version=?,
		updated_at=?,first_seen=?,last_seen=?,merged_into=?,sources=? WHERE id=?`,
		i.Status, i.Severity, i.OwnerGroup, i.Version, i.UpdatedAt.UnixMilli(), i.FirstSeen.UnixMilli(),
		i.LastSeen.UnixMilli(), i.MergedInto, sources, i.ID)
	return err
}

func (s *Store) Update(ctx context.Context, id, actor string, version int64, input UpdateRequest, now time.Time) (Incident, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback()
	i, err := lockedIncident(ctx, tx, id, version)
	if err != nil {
		return Incident{}, err
	}
	before := i.Status
	if before == Closed || (input.Status != "" && !allowedTransition(before, input.Status)) {
		return Incident{}, ErrConflict
	}
	if input.Status != "" {
		i.Status = input.Status
	}
	if input.Severity != "" {
		i.Severity = input.Severity
	}
	if input.OwnerGroup != nil {
		i.OwnerGroup = *input.OwnerGroup
	}
	i.UpdatedAt = now
	if err := persist(ctx, tx, &i); err != nil {
		return Incident{}, err
	}
	if err := s.record(ctx, tx, i, actor, "updated", input.Reason, before, i.Status); err != nil {
		return Incident{}, err
	}
	return i, tx.Commit()
}
