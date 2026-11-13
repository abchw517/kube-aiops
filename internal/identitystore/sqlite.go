package identitystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"

	"github.com/abchw517/kube-aiops/internal/identity"
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
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
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
