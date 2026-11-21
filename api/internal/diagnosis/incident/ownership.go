package incident

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) Merge(ctx context.Context, target, actor string, version int64, input MergeRequest, now time.Time) (Incident, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback()
	to, err := lockedIncident(ctx, tx, target, version)
	if err != nil {
		return Incident{}, err
	}
	from, err := lockedIncident(ctx, tx, input.SourceID, input.SourceVersion)
	if err != nil {
		return Incident{}, err
	}
	if to.ID == from.ID || to.Scope != from.Scope || to.ResourceID != from.ResourceID || to.Status == Closed || from.Status == Closed {
		return Incident{}, ErrConflict
	}
	for _, q := range []string{
		"UPDATE diagnostic_signal SET incident_id=? WHERE incident_id=?",
		"UPDATE diagnostic_evidence SET incident_id=? WHERE incident_id=?",
	} {
		if _, err := tx.ExecContext(ctx, q, to.ID, from.ID); err != nil {
			return Incident{}, err
		}
	}
	before := from.Status
	from.Status, from.MergedInto, from.UpdatedAt = Closed, to.ID, now
	if from.FirstSeen.Before(to.FirstSeen) {
		to.FirstSeen = from.FirstSeen
	}
	if from.LastSeen.After(to.LastSeen) {
		to.LastSeen = from.LastSeen
	}
	to.UpdatedAt = now
	for _, v := range []*Incident{&from, &to} {
		if err := persist(ctx, tx, v); err != nil {
			return Incident{}, err
		}
	}
	if err := s.record(ctx, tx, from, actor, "merged", input.Reason, before, to.DisplayKey); err != nil {
		return Incident{}, err
	}
	if err := s.record(ctx, tx, to, actor, "merged_into", input.Reason, from.DisplayKey, to.Status); err != nil {
		return Incident{}, err
	}
	return to, tx.Commit()
}

func (s *Store) Split(ctx context.Context, parent, actor string, version int64, input SplitRequest, now time.Time) (Incident, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback()
	from, err := lockedIncident(ctx, tx, parent, version)
	if err != nil {
		return Incident{}, err
	}
	if from.Status == Closed {
		return Incident{}, ErrConflict
	}

	signals := make(map[string]struct{}, len(input.EvidenceIDs))
	var earliest, latest time.Time
	for _, key := range input.EvidenceIDs {
		var signalID string
		var first, last int64
		err := tx.QueryRowContext(ctx, `SELECT s.id,s.first_seen,s.last_seen FROM diagnostic_evidence e
			JOIN diagnostic_signal s ON e.signal_id=s.id WHERE e.incident_id=? AND s.incident_id=? AND e.id=?`, parent, parent, key).
			Scan(&signalID, &first, &last)
		if errors.Is(err, sql.ErrNoRows) {
			return Incident{}, ErrNotFound
		}
		if err != nil {
			return Incident{}, err
		}
		signals[signalID] = struct{}{}
		start, end := time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
		if earliest.IsZero() || start.Before(earliest) {
			earliest = start
		}
		if end.After(latest) {
			latest = end
		}
	}
	child := Incident{ID: rand.Text(), Title: input.Title, Scope: from.Scope, Resource: from.Resource, ResourceID: from.ResourceID,
		FindingID: from.FindingID, AnchorAt: from.AnchorAt, Status: Pending, Severity: from.Severity, Source: "split", CreatedAt: now, UpdatedAt: now, FirstSeen: earliest, LastSeen: latest}
	child, err = insert(ctx, tx, child)
	if err != nil {
		return Incident{}, err
	}
	for id := range signals {
		for _, q := range []string{
			"UPDATE diagnostic_signal SET incident_id=? WHERE id=?",
			"UPDATE diagnostic_evidence SET incident_id=? WHERE signal_id=?",
		} {
			if _, err := tx.ExecContext(ctx, q, child.ID, id); err != nil {
				return Incident{}, err
			}
		}
	}
	var first, last int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(first_seen),?),COALESCE(MAX(last_seen),?)
		FROM diagnostic_signal WHERE incident_id=?`, from.CreatedAt.UnixMilli(), from.CreatedAt.UnixMilli(), from.ID).Scan(&first, &last); err != nil {
		return Incident{}, err
	}
	from.FirstSeen, from.LastSeen, from.UpdatedAt = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC(), now
	if err := persist(ctx, tx, &from); err != nil {
		return Incident{}, err
	}
	if err := s.record(ctx, tx, from, actor, "split", input.Reason, from.Status, child.DisplayKey); err != nil {
		return Incident{}, err
	}
	if err := s.record(ctx, tx, child, actor, "split_created", input.Reason, from.DisplayKey, child.Status); err != nil {
		return Incident{}, err
	}
	return child, tx.Commit()
}
