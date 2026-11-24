package incident

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
)

const zabbixSchema = `
CREATE TABLE IF NOT EXISTS diagnostic_zabbix_source (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	endpoint TEXT NOT NULL, environment TEXT NOT NULL, business TEXT NOT NULL,
	groups_json TEXT NOT NULL, auth_method TEXT NOT NULL, upstream_version TEXT NOT NULL,
	credential BLOB NOT NULL, enabled INTEGER NOT NULL, version INTEGER NOT NULL,
	health TEXT NOT NULL DEFAULT 'pending', snapshot TEXT NOT NULL DEFAULT '',
	checked_at INTEGER, synced_at INTEGER, due_at INTEGER NOT NULL DEFAULT 0, lease_until INTEGER NOT NULL DEFAULT 0,
	UNIQUE(endpoint, environment, business)
);
CREATE INDEX IF NOT EXISTS diagnostic_zabbix_queue ON diagnostic_zabbix_source(enabled,due_at);
`

const zabbixColumns = `id,name,endpoint,environment,business,groups_json,auth_method,upstream_version,credential,enabled,version,health,snapshot,checked_at,synced_at,lease_until`
const zabbixInterval = time.Minute

func migrateZabbixSourceName(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info('diagnostic_zabbix_source') WHERE name='name')").Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := tx.ExecContext(ctx, "ALTER TABLE diagnostic_zabbix_source ADD COLUMN name TEXT NOT NULL DEFAULT ''")
	return err
}

type ZabbixScope struct {
	Environment string `json:"environmentId"`
	Business    string `json:"businessId"`
}

type ZabbixOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ZabbixScopeOptions struct {
	Environments []ZabbixOption `json:"environments"`
	Businesses   []ZabbixOption `json:"businesses"`
}

type ZabbixSource struct {
	EnvironmentName string                  `json:"environmentName,omitempty"`
	BusinessName    string                  `json:"businessName,omitempty"`
	ID              string                  `json:"id"`
	Name            string                  `json:"name,omitempty"`
	Endpoint        string                  `json:"endpoint"`
	Scope           ZabbixScope             `json:"scope"`
	Groups          []assetmeta.ZabbixGroup `json:"groups"`
	AuthMethod      string                  `json:"authMethod"`
	UpstreamVersion string                  `json:"upstreamVersion"`
	Enabled         bool                    `json:"enabled"`
	Version         int64                   `json:"version"`
	Health          string                  `json:"health"`
	CheckedAt       *time.Time              `json:"checkedAt,omitempty"`
	SyncedAt        *time.Time              `json:"syncedAt,omitempty"`
	HostCount       *int                    `json:"hostCount,omitempty"`
	ProblemCount    *int                    `json:"problemCount,omitempty"`
}

type zabbixSourceRecord struct {
	ZabbixSource
	credential []byte
	snapshot   *assetmeta.ZabbixSnapshot
	lease      int64
}

func scanZabbix(row rowScanner) (zabbixSourceRecord, error) {
	var d zabbixSourceRecord
	var groups, snapshot string
	var checked, synced sql.NullInt64
	err := row.Scan(&d.ID, &d.Name, &d.Endpoint, &d.Scope.Environment, &d.Scope.Business, &groups, &d.AuthMethod, &d.UpstreamVersion, &d.credential,
		&d.Enabled, &d.Version, &d.Health, &snapshot, &checked, &synced, &d.lease)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(groups), &d.Groups); err != nil {
		return d, err
	}
	d.CheckedAt, d.SyncedAt = nullableTime(checked), nullableTime(synced)
	if snapshot != "" && d.Enabled && d.Health != "restricted" {
		if err = json.Unmarshal([]byte(snapshot), &d.snapshot); err != nil {
			return d, err
		}
		hosts, count := len(d.snapshot.Hosts), 0
		for _, item := range d.snapshot.Problems {
			if !item.SuppressedOrSymptom() && !item.Recovered() {
				count++
			}
		}
		d.HostCount, d.ProblemCount = &hosts, &count
	}
	if !d.Enabled {
		d.Health = "paused"
	} else if d.lease > nowUTC().UnixMilli() {
		d.Health = "syncing"
	} else if d.Health == "available" && d.SyncedAt != nil && nowUTC().Sub(*d.SyncedAt) > 3*zabbixInterval {
		d.Health = "stale"
	}
	return d, nil
}

func zabbixByID(ctx context.Context, db queryer, id string) (zabbixSourceRecord, error) {
	return scanZabbix(db.QueryRowContext(ctx, "SELECT "+zabbixColumns+" FROM diagnostic_zabbix_source WHERE id=?", id))
}

func (s *Store) saveZabbix(ctx context.Context, d zabbixSourceRecord, actor string, create, clear bool) (ZabbixSource, error) {
	groups, err := json.Marshal(d.Groups)
	if err != nil {
		return ZabbixSource{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ZabbixSource{}, err
	}
	defer tx.Rollback()
	if create {
		res, execErr := tx.ExecContext(ctx, `INSERT INTO diagnostic_zabbix_source
		(id,name,endpoint,environment,business,groups_json,auth_method,upstream_version,credential,enabled,version)
		VALUES(?,?,?,?,?,?,?,?,?,?,1) ON CONFLICT(endpoint,environment,business) DO NOTHING`,
			d.ID, d.Name, d.Endpoint, d.Scope.Environment, d.Scope.Business, string(groups), d.AuthMethod, d.UpstreamVersion, d.credential, d.Enabled)
		if err = affected(res, execErr, ErrConflict); err != nil {
			return ZabbixSource{}, err
		}
	} else {
		res, execErr := tx.ExecContext(ctx, `UPDATE diagnostic_zabbix_source SET groups_json=?,auth_method=?,upstream_version=?,credential=?,
			enabled=?,version=version+1,health=CASE WHEN ? THEN 'pending' WHEN health='restricted' AND ? THEN 'pending' ELSE health END,
			snapshot=CASE WHEN ? THEN '' ELSE snapshot END, synced_at=CASE WHEN ? THEN NULL ELSE synced_at END,
			lease_until=0,due_at=0 WHERE id=? AND version=?`,
			string(groups), d.AuthMethod, d.UpstreamVersion, d.credential, d.Enabled, clear, d.Enabled, clear, clear, d.ID, d.Version)
		if err = affected(res, execErr, ErrVersion); err != nil {
			return ZabbixSource{}, err
		}
	}
	if s.audit != nil {
		if err = s.audit(ctx, tx, actor, "zabbix_source_saved", d.ID); err != nil {
			return ZabbixSource{}, err
		}
	}
	out, err := zabbixByID(ctx, tx, d.ID)
	if err != nil {
		return ZabbixSource{}, err
	}
	return out.ZabbixSource, tx.Commit()
}

func affected(result sql.Result, err, empty error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return empty
	}
	return err
}

func (s *Store) queueZabbix(ctx context.Context, d zabbixSourceRecord, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE diagnostic_zabbix_source SET due_at=0 WHERE id=? AND version=? AND enabled=1`, d.ID, d.Version)
	if err = affected(res, err, ErrConflict); err != nil {
		return err
	}
	if s.audit != nil {
		if err = s.audit(ctx, tx, actor, "zabbix_sync_requested", d.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) claimZabbix(ctx context.Context) (zabbixSourceRecord, error) {
	now := nowUTC()
	return scanZabbix(s.db.QueryRowContext(ctx, `UPDATE diagnostic_zabbix_source SET lease_until=?, checked_at=?
		WHERE id=(SELECT id FROM diagnostic_zabbix_source WHERE enabled=1 AND due_at<=? AND lease_until<=? ORDER BY due_at,id LIMIT 1)
		RETURNING `+zabbixColumns, now.Add(45*time.Second).UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli()))
}

func (s *Store) finishZabbix(ctx context.Context, d zabbixSourceRecord, snapshot assetmeta.ZabbixSnapshot, upstreamErr error) error {
	now := nowUTC()
	var data any
	var synced any
	health := "available"
	clear := false
	if upstreamErr == nil {
		snapshot = pruneZabbix(snapshot, d.snapshot, now)
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		data, synced = string(raw), now.UnixMilli()
	} else if errors.Is(upstreamErr, assetmeta.ErrAuthorization) || errors.Is(upstreamErr, assetmeta.ErrZabbixScope) ||
		errors.Is(upstreamErr, assetmeta.ErrConfiguration) {
		health, clear = "restricted", true
	} else if d.snapshot != nil {
		health = "stale"
		if snapshot.HostsVerified {
			allowed := map[string]bool{}
			for _, h := range snapshot.Hosts {
				allowed[h.ID] = true
			}
			stale := *d.snapshot
			stale.Hosts = slices.DeleteFunc(slices.Clone(stale.Hosts), func(h assetmeta.ZabbixHost) bool { return !allowed[h.ID] })
			stale.Problems = make([]assetmeta.ZabbixProblem, 0, len(d.snapshot.Problems))
			for _, row := range d.snapshot.Problems {
				row.Hosts = slices.DeleteFunc(slices.Clone(row.Hosts), func(h assetmeta.ZabbixHost) bool { return !allowed[h.ID] })
				if len(row.Hosts) > 0 {
					stale.Problems = append(stale.Problems, row)
				}
			}
			raw, err := json.Marshal(stale)
			if err != nil {
				return err
			}
			data = string(raw)
		}
	} else {
		health = "unavailable"
	}
	res, err := s.db.ExecContext(ctx, `UPDATE diagnostic_zabbix_source SET health=?,snapshot=CASE WHEN ? THEN '' ELSE COALESCE(?,snapshot) END,
		synced_at=COALESCE(?,synced_at),due_at=?,lease_until=0 WHERE id=? AND version=? AND lease_until=? AND enabled=1`,
		health, clear, data, synced, now.Add(zabbixInterval).UnixMilli(), d.ID, d.Version, d.lease)
	return affected(res, err, ErrVersion)
}

func pruneZabbix(next assetmeta.ZabbixSnapshot, previous *assetmeta.ZabbixSnapshot, now time.Time) assetmeta.ZabbixSnapshot {
	seen := map[string]bool{}
	hosts := map[string]assetmeta.ZabbixHost{}
	for _, h := range next.Hosts {
		hosts[h.ID] = h
	}
	rows := next.Problems[:0]
	for _, row := range next.Problems {
		if row.Recovered() && !recentRecovery(row, now) || seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		rows = append(rows, row)
	}
	if previous != nil {
		for _, row := range previous.Problems {
			if seen[row.ID] || !row.Recovered() || !recentRecovery(row, now) {
				continue
			}
			known := make([]assetmeta.ZabbixHost, 0, len(row.Hosts))
			for _, host := range row.Hosts {
				if h, ok := hosts[host.ID]; ok {
					known = append(known, h)
				}
			}
			row.Hosts = known
			if len(row.Hosts) > 0 {
				seen[row.ID] = true
				rows = append(rows, row)
			}
		}
	}
	slices.SortStableFunc(rows, func(a, b assetmeta.ZabbixProblem) int {
		if a.Recovered() != b.Recovered() {
			if a.Recovered() {
				return 1
			}
			return -1
		}
		if a.Recovered() {
			at, _ := strconv.ParseInt(a.RecoveryClock, 10, 64)
			bt, _ := strconv.ParseInt(b.RecoveryClock, 10, 64)
			if at > bt {
				return -1
			}
			if at < bt {
				return 1
			}
		}
		return 0
	})
	recovered := 0
	next.Problems = rows[:0]
	for _, row := range rows {
		if row.Recovered() {
			recovered++
			if recovered > 500 {
				next.HistoricTruncated = true
				break
			}
		}
		next.Problems = append(next.Problems, row)
	}
	return next
}

func recentRecovery(p assetmeta.ZabbixProblem, now time.Time) bool {
	n, err := strconv.ParseInt(p.RecoveryClock, 10, 64)
	return err == nil && n >= now.Add(-24*time.Hour).Unix()
}

func zabbixID() string { return rand.Text() }
