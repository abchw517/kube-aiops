package identitystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"

	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	_ "modernc.org/sqlite"
)

type Store struct{ *sql.DB }

func Open(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "_pragma=busy_timeout(5000)&_pragma=secure_delete(ON)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS managed_admin (id INTEGER PRIMARY KEY CHECK (id = 1), password_hash TEXT NOT NULL, initial INTEGER NOT NULL);
        CREATE TABLE IF NOT EXISTS ldap_settings (id INTEGER PRIMARY KEY CHECK (id = 1), document TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS local_accounts (
			username TEXT PRIMARY KEY NOT NULL,
			password_hash TEXT NOT NULL,
			groups_json TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS ldap_accounts (
			subject TEXT PRIMARY KEY NOT NULL,
			username TEXT NOT NULL,
			groups_json TEXT NOT NULL,
			last_login TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS platform_identity (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			document TEXT NOT NULL
		);
		INSERT OR IGNORE INTO platform_identity VALUES (1, '{}');
		CREATE TABLE IF NOT EXISTS operation_audit (
			id INTEGER PRIMARY KEY,
			created_at INTEGER NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			risk TEXT NOT NULL,
			target TEXT NOT NULL,
			result TEXT NOT NULL,
			reason TEXT NOT NULL,
			batch TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS operation_audit_batch ON operation_audit(batch);

		CREATE TABLE IF NOT EXISTS resource_catalog_initialized (
			id INTEGER PRIMARY KEY CHECK (id = 1)
		);
		CREATE TABLE IF NOT EXISTS resource_catalog (
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			name TEXT NOT NULL,
			PRIMARY KEY(kind, id)
		);
		CREATE TABLE IF NOT EXISTS resource_catalog_names (
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			id TEXT NOT NULL,
			PRIMARY KEY(kind, name)
		);
		CREATE TABLE IF NOT EXISTS resource_catalog_ids (
			kind TEXT NOT NULL,
			alias TEXT NOT NULL,
			id TEXT NOT NULL,
			stable_key TEXT NOT NULL,
			PRIMARY KEY(kind, alias)
		);
		INSERT OR IGNORE INTO resource_catalog_ids(kind,alias,id,stable_key)
		SELECT kind,id,id,id FROM resource_catalog;

		CREATE TABLE IF NOT EXISTS namespace_business (
			cluster TEXT NOT NULL,
			namespace TEXT NOT NULL,
			business TEXT NOT NULL,
			PRIMARY KEY(cluster, namespace, business)
		);

		CREATE TABLE IF NOT EXISTS managed_clusters (
			id TEXT PRIMARY KEY,
			document TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			snapshot TEXT NOT NULL DEFAULT '[]',
			reported_at INTEGER NOT NULL DEFAULT 0,
			captured_at INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS retired_clusters (id TEXT PRIMARY KEY);

		CREATE TABLE IF NOT EXISTS cluster_collection (
			id TEXT PRIMARY KEY,
			first_at INTEGER NOT NULL DEFAULT 0,
			validated_at INTEGER NOT NULL DEFAULT 0,
			checked_at INTEGER NOT NULL DEFAULT 0,
			document TEXT NOT NULL DEFAULT '{}'
		);
		INSERT OR IGNORE INTO cluster_collection(id, first_at)
		SELECT id, reported_at FROM managed_clusters
		WHERE reported_at > 0 AND EXISTS (
			SELECT 1 FROM json_each(snapshot) WHERE json_extract(value, '$.source') = 'k8sgpt'
		);
		CREATE TRIGGER IF NOT EXISTS cluster_snapshot_collected AFTER UPDATE OF snapshot ON managed_clusters
		WHEN EXISTS (SELECT 1 FROM json_each(NEW.snapshot) WHERE json_extract(value, '$.source') = 'k8sgpt')
		BEGIN
			INSERT INTO cluster_collection(id, first_at) VALUES (NEW.id, NEW.reported_at)
			ON CONFLICT(id) DO UPDATE SET first_at = excluded.first_at WHERE cluster_collection.first_at = 0;
		END;

		CREATE TABLE IF NOT EXISTS finding_reviews (
			finding_key TEXT NOT NULL,
			revision INTEGER NOT NULL,
			conclusion TEXT NOT NULL,
			PRIMARY KEY(finding_key, revision)
		);
		CREATE TABLE IF NOT EXISTS wiki_review_drafts (
			id TEXT PRIMARY KEY,
			finding_key TEXT NOT NULL,
			owner TEXT NOT NULL,
			revision INTEGER NOT NULL,
			conclusion TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS wiki_documents (
			id TEXT PRIMARY KEY NOT NULL,
			cluster TEXT NOT NULL,
			namespace TEXT NOT NULL,
			environment TEXT NOT NULL,
			business TEXT NOT NULL,
			document_type TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS wiki_documents_scope ON wiki_documents(cluster, namespace, environment, business, document_type);
		CREATE TABLE IF NOT EXISTS wiki_revisions (
			knowledge_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			document TEXT NOT NULL,
			PRIMARY KEY(knowledge_id, revision)
		);
		CREATE TABLE IF NOT EXISTS wiki_sources (
			id TEXT PRIMARY KEY NOT NULL,
			url TEXT NOT NULL,
			cluster TEXT NOT NULL,
			namespace TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS wiki_remote_pages (
			source_id TEXT NOT NULL,
			page_id TEXT NOT NULL,
			knowledge_id TEXT NOT NULL,
			source_url TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			missing INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(source_id, page_id)
		);
		CREATE TABLE IF NOT EXISTS wiki_sync_schedules (
			source_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			subject TEXT NOT NULL,
			minutes INTEGER NOT NULL,
			cron TEXT NOT NULL,
			time_zone TEXT NOT NULL,
			next_at INTEGER NOT NULL,
			last_result TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS wiki_source_credentials (
			source_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			subject TEXT NOT NULL,
			username TEXT NOT NULL,
			password_ciphertext BLOB NOT NULL,
			PRIMARY KEY(source_id, provider, subject)
		);
		CREATE TABLE IF NOT EXISTS authorization_roles (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			document TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS authorization_policy (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			document TEXT NOT NULL
		);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// Import replaces the policy and, when supplied, the local accounts in one transaction.
func (s *Store) Import(ctx context.Context, accounts []identity.LocalAccount, policy []byte) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if accounts != nil {
		if _, err := tx.ExecContext(ctx, "DELETE FROM local_accounts"); err != nil {
			return err
		}
		for _, account := range accounts {
			groups, err := json.Marshal(account.Groups)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO local_accounts VALUES (?, ?, ?)", account.Username, account.Hash, string(groups)); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO authorization_policy VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET document=excluded.document`, string(policy)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Policy(ctx context.Context) ([]byte, error) {
	var policy []byte
	err := s.QueryRowContext(ctx, "SELECT document FROM authorization_policy WHERE id=1").Scan(&policy)
	return policy, err
}

func (s *Store) Roles(ctx context.Context) ([]byte, error) {
	var roles []byte
	err := s.QueryRowContext(ctx, "SELECT document FROM authorization_roles WHERE id=1").Scan(&roles)
	return roles, err
}

func (s *Store) SaveRoles(ctx context.Context, roles []byte) error {
	_, err := s.ExecContext(ctx, `INSERT INTO authorization_roles VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET document=excluded.document`, string(roles))
	return err
}

func (s *Store) LocalAccount(ctx context.Context, username string) (identity.LocalAccount, error) {
	account := identity.LocalAccount{Username: username}
	var groups []byte
	err := s.QueryRowContext(ctx, "SELECT password_hash, groups_json FROM local_accounts WHERE username=?", username).Scan(&account.Hash, &groups)
	if err != nil {
		return account, err
	}
	return account, json.Unmarshal(groups, &account.Groups)
}

func (s *Store) RecordLDAPLogin(ctx context.Context, username string, principal identity.Principal) error {
	groups, err := json.Marshal(principal.Groups)
	if err != nil {
		return err
	}
	_, err = s.ExecContext(ctx, `INSERT INTO ldap_accounts VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(subject) DO UPDATE SET username=excluded.username, groups_json=excluded.groups_json, last_login=excluded.last_login`,
		principal.Subject, username, string(groups))
	return err
}

func (s *Store) Admin(ctx context.Context) (hash string, initial bool, err error) {
	err = s.QueryRowContext(ctx, "SELECT password_hash, initial FROM managed_admin WHERE id=1").Scan(&hash, &initial)
	return
}

func (s *Store) BootstrapAdmin(ctx context.Context, hash string) error {
	_, err := s.ExecContext(ctx, "INSERT OR IGNORE INTO managed_admin VALUES (1, ?, 1)", hash)
	return err
}

func (s *Store) ChangeAdmin(ctx context.Context, previous, next string) (bool, error) {
	result, err := s.ExecContext(ctx, "UPDATE managed_admin SET password_hash=?, initial=0 WHERE id=1 AND password_hash=?", next, previous)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
