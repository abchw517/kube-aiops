package incident

import (
	"context"
	"net/http"

	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func (s *Server) deleteZabbix(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	source, err := s.sourceZabbix(r.Context(), p, r.PathValue("sourceId"), ConnectorDelete)
	if err != nil {
		return err
	}
	return s.deleteSource(w, r, p, source.ID, source.Version, "zabbix_source_deleted",
		"DELETE FROM diagnostic_zabbix_source WHERE id=? AND version=?")
}

func (s *Server) deleteObservability(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	source, err := s.Store.obsByID(ctx, r.PathValue("sourceId"))
	if err != nil {
		return err
	}
	if err := s.obsScope(ctx, p, ConnectorDelete, source.Scope); err != nil {
		return err
	}
	return s.deleteSource(w, r, p, source.ID, source.Version, "observability.delete",
		"DELETE FROM diagnostic_observability WHERE id=? AND version=?")
}

func (s *Server) deleteDatasource(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	source, err := s.getDatasource(r.Context(), p, r.PathValue("datasourceId"), ConnectorDelete)
	if err != nil {
		return err
	}
	return s.deleteSource(w, r, p, source.ID, source.Version, "datasource_deleted",
		"DELETE FROM diagnostic_datasource WHERE id=? AND version=?")
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request, p identity.Principal, id string, currentVersion int64, operation, query string) error {
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	if version != currentVersion {
		return ErrVersion
	}
	if err := s.Store.removeSource(r.Context(), id, version, p.Subject, operation, query); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Store) removeSource(ctx context.Context, id string, version int64, actor, operation, query string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, query, id, version)
	if err = affected(result, err, ErrVersion); err != nil {
		return err
	}
	if operation == "datasource_deleted" {
		var referenced bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM diagnostic_asset a
			JOIN diagnostic_dependency d ON d.target_kind='asset' AND d.target_id=a.id
			WHERE a.datasource_id=?)`, id).Scan(&referenced); err != nil {
			return err
		}
		if referenced {
			return httpError{409, "DATASOURCE_ASSETS_REFERENCED", "该数据源的资产已关联故障，暂不能删除。可先停用该数据源。"}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM diagnostic_asset WHERE datasource_id=?", id); err != nil {
			return err
		}
	}
	if s.audit != nil {
		if err := s.audit(ctx, tx, actor, operation, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
