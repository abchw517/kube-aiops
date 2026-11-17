package portal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

const (
	environmentKind = "environments"
	businessKind    = "business-lines"
)

type catalogEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func initResourceCatalog(ctx context.Context, store *identitystore.Store) error {
	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A separate marker lets operators rename or delete a preset without startup restoring it.
	var initialized bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM resource_catalog_initialized)").Scan(&initialized); err != nil {
		return err
	}
	if !initialized {
		for _, entry := range []catalogEntry{{"development", "研发"}, {"testing", "测试"}, {"staging", "预发布"}, {"production", "生产"}} {
			if err := writeCatalogEntry(ctx, tx, environmentKind, entry, true); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO resource_catalog_initialized(id) VALUES (1)"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writeCatalogEntry(ctx context.Context, tx *sql.Tx, kind string, entry catalogEntry, create bool) error {
	if create {
		if _, err := tx.ExecContext(ctx, "INSERT INTO resource_catalog(kind,id,name) VALUES (?,?,?)", kind, entry.ID, entry.Name); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, "UPDATE resource_catalog SET name=? WHERE kind=? AND id=?", entry.Name, kind, entry.ID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO resource_catalog_names(kind,name,id) VALUES (?,?,?) ON CONFLICT(kind,name) DO NOTHING", kind, entry.Name, entry.ID)
	return err
}

func readCatalog(ctx context.Context, store *identitystore.Store, kind string) ([]catalogEntry, error) {
	rows, err := store.QueryContext(ctx, "SELECT id,name FROM resource_catalog WHERE kind=? ORDER BY name,id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]catalogEntry, 0)
	for rows.Next() {
		var entry catalogEntry
		if err := rows.Scan(&entry.ID, &entry.Name); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func validCatalogName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= 128 && !strings.ContainsFunc(name, unicode.IsControl)
}

func (o *onboarding) catalog(w http.ResponseWriter, r *http.Request, kind string) {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		entries, err := readCatalog(ctx, o.store, kind)
		if err != nil {
			http.Error(w, "资源目录暂不可用", http.StatusServiceUnavailable)
			return
		}
		adminhttp.WriteJSON(w, entries)
		return
	}
	create, remove := r.Method == http.MethodPost, r.Method == http.MethodDelete
	if !create && !remove && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, POST, PUT, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var entry catalogEntry
	if !adminhttp.Decode(w, r, &entry) {
		http.Error(w, "资源目录请求无效", http.StatusBadRequest)
		return
	}
	entry.Name = strings.TrimSpace(entry.Name)
	if (!remove && !validCatalogName(entry.Name)) || (create && entry.ID != "") || (!create && entry.ID == "") {
		http.Error(w, "资源目录请求无效", http.StatusBadRequest)
		return
	}

	// Serialize against legacy cluster writes while these still reference labels.
	o.mu.Lock()
	defer o.mu.Unlock()
	tx, err := o.store.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "资源目录暂不可用", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback()
	fail := func() { http.Error(w, "资源目录暂不可用", http.StatusServiceUnavailable) }
	if !create {
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT name FROM resource_catalog WHERE kind=? AND id=?", kind, entry.ID).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "条目不存在", http.StatusNotFound)
			return
		}
		if err != nil {
			fail()
			return
		}
	}
	if remove {
		references, err := o.catalogReferences(ctx, tx, kind, entry.ID)
		if err != nil {
			fail()
			return
		}
		if len(references) != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			adminhttp.WriteJSON(w, map[string]any{"message": "仍有集群引用", "references": references})
			return
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM resource_catalog_names WHERE kind=? AND id=?", kind, entry.ID); err != nil {
			fail()
			return
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM resource_catalog WHERE kind=? AND id=?", kind, entry.ID); err != nil {
			fail()
			return
		}
		if err := tx.Commit(); err != nil {
			fail()
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var owner string
	err = tx.QueryRowContext(ctx, "SELECT id FROM resource_catalog_names WHERE kind=? AND name=?", kind, entry.Name).Scan(&owner)
	if err == nil && owner != entry.ID {
		http.Error(w, "名称已被使用", http.StatusConflict)
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail()
		return
	}
	if create {
		entry.ID = rand.Text()
	}
	if err := writeCatalogEntry(ctx, tx, kind, entry, create); err != nil {
		fail()
		return
	}
	if err := tx.Commit(); err != nil {
		fail()
		return
	}
	adminhttp.WriteJSON(w, entry)
}

func (o *onboarding) catalogReferences(ctx context.Context, tx *sql.Tx, kind, id string) ([]string, error) {
	field := "environment"
	if kind == businessKind {
		field = "business"
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM managed_clusters
		WHERE json_extract(document, '$.' || ?) IN
			(SELECT name FROM resource_catalog_names WHERE kind=? AND id=?)`, field, kind, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var cluster string
		if err := rows.Scan(&cluster); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, cluster)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	aliases, err := tx.QueryContext(ctx, "SELECT name FROM resource_catalog_names WHERE kind=? AND id=?", kind, id)
	if err != nil {
		return nil, err
	}
	defer aliases.Close()
	names := make(map[string]bool)
	for aliases.Next() {
		var name string
		if err := aliases.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	if err := aliases.Err(); err != nil {
		return nil, err
	}
	if o.registry != nil {
		static := append([]kubernetes.Cluster{}, o.registry.Backend.Clusters()...)
		for _, target := range o.registry.Targets {
			static = append(static, target.Backend.Clusters()...)
		}
		for _, cluster := range static {
			label := cluster.Environment
			if kind == businessKind {
				label = cluster.Business
			}
			if names[label] {
				ids = append(ids, cluster.ID)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}
