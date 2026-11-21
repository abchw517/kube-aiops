package incident

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
)

func (s *Store) Evidence(ctx context.Context, id string, after cursor, limit int) (Page[Evidence], error) {
	page := Page[Evidence]{Items: []Evidence{}}
	where := "incident_id=?"
	args := []any{id}
	if after.ID != "" {
		where += " AND (observed_at,id)<(?,?)"
		args = append(args, after.Time, after.ID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,source,kind,state,observed_at,collected_at,summary
		FROM diagnostic_evidence WHERE `+where+" ORDER BY observed_at DESC,id DESC LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Evidence
		var observed, collected int64
		if err := rows.Scan(&v.ID, &v.Source, &v.Kind, &v.State, &observed, &collected, &v.Summary); err != nil {
			return page, err
		}
		v.ObservedAt, v.CollectedAt = time.UnixMilli(observed).UTC(), time.UnixMilli(collected).UTC()
		page.Items = append(page.Items, v)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.Pagination.Continue = encodeCursor(cursor{ID: last.ID, Time: last.ObservedAt.UnixMilli(), From: "evidence:" + id})
	}
	return page, rows.Err()
}

func (s *Store) EvidenceDetail(ctx context.Context, id, evidenceID string) (EvidenceDetail, error) {
	var out EvidenceDetail
	var observed, collected int64
	var document []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,source,kind,state,observed_at,collected_at,summary,document
		FROM diagnostic_evidence WHERE incident_id=? AND id=?`, id, evidenceID).
		Scan(&out.ID, &out.Source, &out.Kind, &out.State, &observed, &collected, &out.Summary, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.ObservedAt, out.CollectedAt = time.UnixMilli(observed).UTC(), time.UnixMilli(collected).UTC()
	err = json.Unmarshal(document, &out.Signal)
	return out, err
}

func (s *Store) Correlate(ctx context.Context, id, actor string, version int64, signals []Signal, sources []correlation.SourceStatus, now time.Time) (Incident, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback()
	i, err := lockedIncident(ctx, tx, id, version)
	if err != nil {
		return Incident{}, err
	}
	if i.Status == Closed {
		return Incident{}, ErrConflict
	}
	changed := 0
	for _, signal := range signals {
		ok, err := acceptSignal(ctx, tx, i, signal, now)
		if err != nil {
			return Incident{}, err
		}
		if !ok {
			continue
		}
		changed++
		if signal.First.Before(i.FirstSeen) {
			i.FirstSeen = signal.First
		}
		if signal.Last.After(i.LastSeen) {
			i.LastSeen = signal.Last
		}
	}
	if changed == 0 && reflect.DeepEqual(i.Sources, sources) {
		return i, nil
	}
	i.Sources, i.UpdatedAt = sources, now
	if err := persist(ctx, tx, &i); err != nil {
		return Incident{}, err
	}
	if err := s.record(ctx, tx, i, actor, "correlated", "", "", ""); err != nil {
		return Incident{}, err
	}
	return i, tx.Commit()
}

func acceptSignal(ctx context.Context, tx *sql.Tx, i Incident, v Signal, now time.Time) (bool, error) {
	var id, owner, oldState string
	var last int64
	err := tx.QueryRowContext(ctx, `SELECT id,incident_id,last_seen,state FROM diagnostic_signal
		WHERE resource_id=? AND source=? AND source_key=?`, i.ResourceID, v.Value.Source, v.Key).Scan(&id, &owner, &last, &oldState)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = rand.Text()
	case err != nil:
		return false, err
	case owner != i.ID || last > v.Last.UnixMilli() || (last == v.Last.UnixMilli() && (oldState == v.State || oldState == "resolved")):
		return false, nil
	}

	data, err := json.Marshal(v.Value)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO diagnostic_signal
		(id,incident_id,resource_id,source,source_key,kind,state,first_seen,last_seen,collected_at,summary,document)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
		first_seen=MIN(first_seen,excluded.first_seen),last_seen=excluded.last_seen,state=excluded.state,
		collected_at=excluded.collected_at,summary=excluded.summary,document=excluded.document`,
		id, i.ID, i.ResourceID, v.Value.Source, v.Key, v.Value.Type, v.State, v.First.UnixMilli(), v.Last.UnixMilli(), now.UnixMilli(), v.Value.Summary, data)
	if err != nil {
		return false, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(append([]byte(v.State+"\x00"), data...)))
	_, err = tx.ExecContext(ctx, `INSERT INTO diagnostic_evidence
		(id,signal_id,incident_id,source,kind,state,first_seen,observed_at,collected_at,summary,document,digest)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(signal_id,digest) DO NOTHING`,
		rand.Text(), id, i.ID, v.Value.Source, v.Value.Type, v.State, v.First.UnixMilli(), v.Last.UnixMilli(), now.UnixMilli(), v.Value.Summary, data, digest)
	return err == nil, err
}
