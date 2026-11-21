package incident

const schemaV2 = `
CREATE TABLE IF NOT EXISTS diagnostic_rca_run (
	id TEXT PRIMARY KEY, incident_id TEXT NOT NULL, requester TEXT NOT NULL, request_key TEXT NOT NULL, digest TEXT NOT NULL,
	actor TEXT NOT NULL, observation TEXT NOT NULL, snapshots TEXT NOT NULL, state TEXT NOT NULL,
	version INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER,
	error_code TEXT NOT NULL DEFAULT '', lease TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0,
	policy_id TEXT NOT NULL DEFAULT '', policy_version INTEGER NOT NULL DEFAULT 0,
	UNIQUE(incident_id,requester,request_key)
);
CREATE UNIQUE INDEX IF NOT EXISTS diagnostic_rca_active ON diagnostic_rca_run(incident_id) WHERE state IN ('queued','running');
CREATE INDEX IF NOT EXISTS diagnostic_rca_queue ON diagnostic_rca_run(state,created_at,id);
CREATE INDEX IF NOT EXISTS diagnostic_rca_incident ON diagnostic_rca_run(incident_id,created_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS diagnostic_rca_revision (
	run_id TEXT NOT NULL, version INTEGER NOT NULL, actor TEXT NOT NULL, at INTEGER NOT NULL, conclusion TEXT NOT NULL,
	PRIMARY KEY(run_id,version)
);
CREATE TABLE IF NOT EXISTS diagnostic_dependency (
	source_id TEXT NOT NULL, target_kind TEXT NOT NULL, target_id TEXT NOT NULL,
	relation TEXT NOT NULL, confidence TEXT NOT NULL, reason TEXT NOT NULL, actor TEXT NOT NULL, recorded_at INTEGER NOT NULL, evidence_id TEXT NOT NULL,
	PRIMARY KEY(source_id,target_kind,target_id,relation)
);
CREATE INDEX IF NOT EXISTS diagnostic_dependency_target ON diagnostic_dependency(target_kind,target_id);
CREATE TABLE IF NOT EXISTS diagnostic_datasource (
	id TEXT PRIMARY KEY, scope TEXT NOT NULL, document TEXT NOT NULL, version INTEGER NOT NULL,
	enabled INTEGER NOT NULL, health TEXT NOT NULL DEFAULT 'untested', checked_at INTEGER, synced_at INTEGER,
	message TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS diagnostic_asset (
	id TEXT PRIMARY KEY, datasource_id TEXT NOT NULL, document TEXT NOT NULL, observed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS diagnostic_asset_datasource ON diagnostic_asset(datasource_id,id);
CREATE TABLE IF NOT EXISTS diagnostic_policy (
	id TEXT PRIMARY KEY, scope TEXT NOT NULL, document TEXT NOT NULL, owner TEXT NOT NULL,
	version INTEGER NOT NULL, enabled INTEGER NOT NULL, effective_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS diagnostic_policy_execution (
	policy_id TEXT NOT NULL, policy_version INTEGER NOT NULL, resource_id TEXT NOT NULL, incident_id TEXT NOT NULL,
	signal_id TEXT NOT NULL, signal_at INTEGER NOT NULL, run_id TEXT NOT NULL, queued_at INTEGER NOT NULL,
	PRIMARY KEY(policy_id,policy_version,signal_id,signal_at)
);
CREATE INDEX IF NOT EXISTS diagnostic_policy_execution_cooldown ON diagnostic_policy_execution(policy_id,resource_id,queued_at);
`
