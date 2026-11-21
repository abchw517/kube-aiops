package incident

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Filter struct {
	Environment, Business, Cluster, Namespace, Status, Severity, Source, Search, Platform string
	From, To                                                                              time.Time
}

type cursor struct {
	Time int64  `json:"t"`
	ID   string `json:"i"`
	From string `json:"f"`
}

func encodeCursor(value cursor) string {
	data, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(raw, filter string) (cursor, bool) {
	if raw == "" {
		return cursor{}, true
	}
	if len(raw) > 512 {
		return cursor{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	var value cursor
	return value, err == nil && json.Unmarshal(data, &value) == nil && value.From == filter && validID(value.ID)
}

func (f Filter) key() string {
	data, _ := json.Marshal(f)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (f Filter) where() (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	for _, field := range []struct{ column, value string }{
		{"environment", f.Environment}, {"business", f.Business}, {"cluster", f.Cluster}, {"namespace", f.Namespace},
		{"status", f.Status}, {"severity", f.Severity},
	} {
		if field.value != "" {
			where = append(where, field.column+"=?")
			args = append(args, field.value)
		}
	}
	if f.Source == "k8sgpt" {
		where = append(where, "finding_id<>''")
	} else if f.Source == "manual" {
		where = append(where, "source='manual'")
	} else if f.Source != "" {
		where = append(where, "EXISTS (SELECT 1 FROM diagnostic_signal s WHERE s.incident_id=diagnostic_incident.id AND s.source=?)")
		args = append(args, f.Source)
	}
	if f.Platform != "" {
		where = append(where, `CASE json_extract(resource,'$.kind') WHEN 'Host' THEN 'zabbix' WHEN 'ECS' THEN 'aliyun-ecs'
			WHEN 'CVM' THEN 'tencent-cvm' ELSE 'kubernetes' END = ?`)
		args = append(args, f.Platform)
	}
	if f.Search != "" {
		where = append(where, "(instr(lower(title),lower(?))>0 OR instr(lower(description),lower(?))>0 OR lower(id)=lower(?))")
		args = append(args, f.Search, f.Search, f.Search)
	}
	if !f.From.IsZero() {
		where = append(where, "last_seen>=?")
		args = append(args, f.From.UnixMilli())
	}
	if !f.To.IsZero() {
		where = append(where, "last_seen<=?")
		args = append(args, f.To.UnixMilli())
	}
	return strings.Join(where, " AND "), args
}

// visit closes each SQLite batch before calling authorization, which can query the same database.
func (s *Store) visit(ctx context.Context, f Filter, after cursor, consume func(Incident) (bool, error)) error {
	const batchSize = 100
	for {
		items, err := s.batch(ctx, f, after, batchSize)
		if err != nil {
			return err
		}
		for _, item := range items {
			more, err := consume(item)
			if err != nil || !more {
				return err
			}
			after = cursor{Time: item.LastSeen.UnixMilli(), ID: item.ID}
		}
		if len(items) < batchSize {
			return nil
		}
	}
}

func (s *Store) batch(ctx context.Context, f Filter, after cursor, max int) ([]Incident, error) {
	where, args := f.where()
	if after.ID != "" {
		where += " AND (last_seen,id)<(?,?)"
		args = append(args, after.Time, after.ID)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM diagnostic_incident WHERE "+where+" ORDER BY last_seen DESC,id DESC LIMIT ?", append(args, max)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Incident, 0, max)
	for rows.Next() {
		item, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Timeline(ctx context.Context, id string, after cursor, limit int) (Page[TimelineEntry], error) {
	page := Page[TimelineEntry]{Items: []TimelineEntry{}}
	before := int64(1<<63 - 1)
	if after.ID != "" {
		var err error
		before, err = strconv.ParseInt(after.ID, 10, 64)
		if err != nil {
			return page, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,at,actor,kind,reason,before_state,after_state FROM diagnostic_timeline
		WHERE incident_id=? AND id<? ORDER BY id DESC LIMIT ?`, id, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var v TimelineEntry
		var at int64
		if err := rows.Scan(&v.ID, &at, &v.Actor, &v.Kind, &v.Reason, &v.Before, &v.After); err != nil {
			return page, err
		}
		v.At = time.UnixMilli(at).UTC()
		page.Items = append(page.Items, v)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.Pagination.Continue = encodeCursor(cursor{ID: strconv.FormatInt(page.Items[limit-1].ID, 10), From: "timeline:" + id})
	}
	return page, rows.Err()
}

func (s *Store) candidates(ctx context.Context, i Incident) ([]Incident, error) {
	start := i.FirstSeen.Add(-30 * time.Minute).UnixMilli()
	end := i.LastSeen.Add(30 * time.Minute).UnixMilli()
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+` FROM diagnostic_incident WHERE resource_id=? AND id<>?
		AND status<>'closed' AND first_seen<=? AND last_seen>=? ORDER BY last_seen DESC,id DESC LIMIT 10`, i.ResourceID, i.ID, end, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		item, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		if item.Scope == i.Scope {
			item.Sources = nil
			out = append(out, item)
		}
	}
	return out, rows.Err()
}

func (s *Store) evidenceCounts(ctx context.Context, id string) (map[string]int, error) {
	var signals, evidence int
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM diagnostic_signal WHERE incident_id=?),
		(SELECT count(*) FROM diagnostic_evidence WHERE incident_id=?)`, id, id).Scan(&signals, &evidence)
	return map[string]int{"signals": signals, "evidence": evidence}, err
}
