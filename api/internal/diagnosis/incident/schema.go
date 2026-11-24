package incident

import (
	"context"
	"database/sql"
)

const schema = `
CREATE TABLE IF NOT EXISTS diagnostic_incident (
	number INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	environment TEXT NOT NULL, business TEXT NOT NULL, cluster TEXT NOT NULL, namespace TEXT NOT NULL,
	resource_id TEXT NOT NULL,
	title TEXT NOT NULL, description TEXT NOT NULL, resource TEXT NOT NULL, finding_id TEXT NOT NULL,
	severity TEXT NOT NULL CHECK (severity IN ('P1','P2','P3')),
	status TEXT NOT NULL CHECK (status IN ('pending','investigating','recovered','closed')),
	owner_group TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL,
	version INTEGER NOT NULL CHECK (version > 0),
	first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, anchor_at INTEGER NOT NULL,
	merged_into TEXT NOT NULL DEFAULT '', sources TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS diagnostic_incident_scope ON diagnostic_incident(cluster,namespace,status,last_seen DESC, id DESC);
CREATE INDEX IF NOT EXISTS diagnostic_incident_order ON diagnostic_incident(last_seen DESC, id DESC);
CREATE TABLE IF NOT EXISTS diagnostic_incident_idempotency (
	actor TEXT NOT NULL, key TEXT NOT NULL, digest TEXT NOT NULL, incident_id TEXT NOT NULL,
	PRIMARY KEY(actor,key)
);
CREATE TABLE IF NOT EXISTS diagnostic_timeline (
	id INTEGER PRIMARY KEY AUTOINCREMENT, incident_id TEXT NOT NULL,
	at INTEGER NOT NULL, actor TEXT NOT NULL, kind TEXT NOT NULL, reason TEXT NOT NULL,
	before_state TEXT NOT NULL, after_state TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS diagnostic_timeline_incident ON diagnostic_timeline(incident_id,id DESC);
CREATE TABLE IF NOT EXISTS diagnostic_signal (
	id TEXT PRIMARY KEY,
	incident_id TEXT NOT NULL, resource_id TEXT NOT NULL, source TEXT NOT NULL, source_key TEXT NOT NULL,
	kind TEXT NOT NULL, state TEXT NOT NULL,
	first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, collected_at INTEGER NOT NULL,
	summary TEXT NOT NULL, document TEXT NOT NULL,
	UNIQUE(resource_id,source,source_key)
);
CREATE INDEX IF NOT EXISTS diagnostic_signal_incident ON diagnostic_signal(incident_id,last_seen DESC,id DESC);
CREATE TABLE IF NOT EXISTS diagnostic_evidence (
	id TEXT PRIMARY KEY, signal_id TEXT NOT NULL, incident_id TEXT NOT NULL,
	source TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL,
	first_seen INTEGER NOT NULL, observed_at INTEGER NOT NULL, collected_at INTEGER NOT NULL,
	summary TEXT NOT NULL, document TEXT NOT NULL, digest TEXT NOT NULL,
	UNIQUE(signal_id,digest)
);
CREATE INDEX IF NOT EXISTS diagnostic_evidence_incident ON diagnostic_evidence(incident_id, observed_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS diagnostic_evidence_signal ON diagnostic_evidence(signal_id);
`

type AuditWriter func(context.Context, *sql.Tx, string, string, string) error

type Store struct {
	db    *sql.DB
	audit AuditWriter
}

func Open(ctx context.Context, db *sql.DB, audit AuditWriter) (*Store, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema+schemaV2+zabbixSchema); err != nil {
		return nil, err
	}
	if err := migrateZabbixSourceName(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Store{db: db, audit: audit}, nil
}
