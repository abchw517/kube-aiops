package incident

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const (
	ConnectorRead  = "diagnosis.connector.read"
	ConnectorWrite = "diagnosis.connector.write"
	ConnectorTest  = "diagnosis.connector.test"
)

type DatasourceInput struct {
	Name          string           `json:"name"`
	Scope         Scope            `json:"scope"`
	Config        assetmeta.Config `json:"config"`
	Enabled       bool             `json:"enabled"`
	CredentialRef string           `json:"credentialRef,omitempty"`
	IngressRef    *string          `json:"ingressRef,omitempty"`
}

type Datasource struct {
	ID                   string           `json:"id"`
	Name                 string           `json:"name"`
	Scope                Scope            `json:"scope"`
	Config               assetmeta.Config `json:"config"`
	Enabled              bool             `json:"enabled"`
	Version              int64            `json:"version"`
	Health               string           `json:"health"`
	Message              string           `json:"message,omitempty"`
	CheckedAt            *time.Time       `json:"checkedAt,omitempty"`
	SyncedAt             *time.Time       `json:"syncedAt,omitempty"`
	CredentialConfigured bool             `json:"credentialConfigured"`
	IngressConfigured    bool             `json:"ingressConfigured"`
}

type inventoryAsset struct {
	assetmeta.Asset
	ID           string    `json:"id"`
	DatasourceID string    `json:"datasourceId"`
	ObservedAt   time.Time `json:"observedAt"`
}

type datasourceRecord struct {
	Datasource
	credential, ingress string
}

const datasourceColumns = `id,scope,document,version,enabled,health,checked_at,synced_at,message`

func scopeKey(s Scope) string {
	return strings.Join([]string{s.EnvironmentID, s.BusinessID, s.ClusterID, s.Namespace}, "/")
}

func readDatasource(row rowScanner) (datasourceRecord, error) {
	var r datasourceRecord
	var document, scope []byte
	var checked, synced sql.NullInt64
	err := row.Scan(&r.ID, &scope, &document, &r.Version, &r.Enabled, &r.Health, &checked, &synced, &r.Message)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	var cfg DatasourceInput
	if err = json.Unmarshal(document, &cfg); err != nil {
		return r, err
	}
	if err = json.Unmarshal(scope, &r.Scope); err != nil {
		return r, err
	}
	r.Name, r.Config, r.credential = cfg.Name, cfg.Config, cfg.CredentialRef
	if cfg.IngressRef != nil {
		r.ingress = *cfg.IngressRef
	}
	r.CredentialConfigured, r.IngressConfigured = r.credential != "", r.ingress != ""
	r.CheckedAt, r.SyncedAt = nullableTime(checked), nullableTime(synced)
	return r, nil
}

func datasourceByID(ctx context.Context, db queryer, id string) (datasourceRecord, error) {
	return readDatasource(db.QueryRowContext(ctx, "SELECT "+datasourceColumns+" FROM diagnostic_datasource WHERE id=?", id))
}

func (s *Server) registerDatasource(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/diagnostics/datasources", s.protect(s.listDatasources))
	mux.HandleFunc("POST /api/v1/diagnostics/datasources", s.protect(s.createDatasource))
	mux.HandleFunc("PATCH /api/v1/diagnostics/datasources/{datasourceId}", s.protect(s.updateDatasource))
	mux.HandleFunc("POST /api/v1/diagnostics/datasources/{datasourceId}/test", s.protect(s.testDatasource))
	mux.HandleFunc("GET /api/v1/diagnostics/datasources/{datasourceId}/assets", s.protect(s.listAssets))
	mux.HandleFunc("POST /api/v1/diagnostics/ingress/{connectorId}/signals", s.ingressSignal)
}

func (s *Server) canonicalScope(ctx context.Context, p identity.Principal, code string, input Scope) (Scope, error) {
	if err := input.Validate(); err != nil {
		return Scope{}, err
	}
	if err := s.require(ctx, p, code, Scope{ClusterID: input.ClusterID, Namespace: input.Namespace}, true); err != nil {
		return Scope{}, err
	}
	if s.ResolveScope != nil {
		out, err := s.ResolveScope(ctx, input)
		if err != nil {
			return Scope{}, err
		}
		input = out
	}
	return input, s.require(ctx, p, code, input, false)
}

func setVersion(w http.ResponseWriter, v int64) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(v, 10)))
}

func (s *Server) listDatasources(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	if err := s.require(ctx, p, ConnectorRead, Scope{}, true); err != nil {
		return err
	}
	after, limit, err := requestPage(r, "datasources")
	if err != nil {
		return err
	}
	page := Page[Datasource]{Items: []Datasource{}}
	err = scanConfigPages(ctx, s.Store.db, "diagnostic_datasource", datasourceColumns, after.ID, readDatasource, func(d datasourceRecord) (bool, error) {
		ok, err := s.Authorize(ctx, p, ConnectorRead, d.Scope, false)
		if err != nil || !ok {
			return true, err
		}
		if len(page.Items) == limit {
			page.Pagination.Continue = encodeCursor(cursor{From: "datasources", ID: page.Items[limit-1].ID})
			return false, nil
		}
		page.Items = append(page.Items, d.Datasource)
		return true, nil
	})
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, page)
	return nil
}

func scanConfigPages[T any](ctx context.Context, db *sql.DB, table, columns, after string, scan func(rowScanner) (T, error), visit func(T) (bool, error)) error {
	const limit = 50
	for {
		rows, err := db.QueryContext(ctx, "SELECT id,"+columns+" FROM "+table+" WHERE id>? ORDER BY id LIMIT ?", after, limit)
		if err != nil {
			return err
		}
		type entry struct {
			id   string
			item T
		}
		items := make([]entry, 0, limit)
		for rows.Next() {
			var id string
			item, err := scan(prependedScanner{rows, &id})
			if err != nil {
				rows.Close()
				return err
			}
			items = append(items, entry{id, item})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range items {
			after = e.id
			more, err := visit(e.item)
			if !more || err != nil {
				return err
			}
		}
		if len(items) < limit {
			return nil
		}
	}
}

type prependedScanner struct {
	row rowScanner
	key *string
}

func (s prependedScanner) Scan(dst ...any) error { return s.row.Scan(append([]any{s.key}, dst...)...) }

func (s *Server) getDatasource(ctx context.Context, p identity.Principal, id, code string) (datasourceRecord, error) {
	d, err := datasourceByID(ctx, s.Store.db, id)
	if err != nil {
		return d, err
	}
	ok, err := s.Authorize(ctx, p, code, d.Scope, false)
	if err != nil {
		return d, err
	}
	if !ok {
		return d, ErrNotFound
	}
	return d, nil
}

func (s *Server) validateDatasource(input *DatasourceInput) error {
	if s.Assets == nil {
		return upstream
	}
	input.Name = cleanText(input.Name, 180)
	if input.Name == "" || !assetmeta.ValidReference(input.CredentialRef) ||
		input.IngressRef != nil && *input.IngressRef != "" && !assetmeta.ValidReference(*input.IngressRef) ||
		s.Assets.Validate(input.Config) != nil {
		return badInput
	}
	return nil
}

func (s *Server) createDatasource(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	var input DatasourceInput
	if !adminhttp.Decode(w, r, &input) {
		return badInput
	}
	scope, err := s.canonicalScope(ctx, p, ConnectorWrite, input.Scope)
	if err != nil {
		return err
	}
	input.Scope = scope
	if err = s.validateDatasource(&input); err != nil {
		return err
	}
	value, err := s.Store.saveDatasource(ctx, input, p.Subject, "", 0)
	if err != nil {
		return err
	}
	setVersion(w, value.Version)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	adminhttp.WriteJSON(w, value)
	return nil
}

func (s *Server) updateDatasource(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	d, err := s.getDatasource(ctx, p, r.PathValue("datasourceId"), ConnectorWrite)
	if err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	var input DatasourceInput
	if !adminhttp.Decode(w, r, &input) || input.Scope != d.Scope || input.Config.Kind != d.Config.Kind {
		return badInput
	}
	if input.CredentialRef == "" {
		input.CredentialRef = d.credential
	}
	if input.IngressRef == nil {
		input.IngressRef = &d.ingress
	}
	if err = s.validateDatasource(&input); err != nil {
		return err
	}
	value, err := s.Store.saveDatasource(ctx, input, p.Subject, d.ID, version)
	if err != nil {
		return err
	}
	setVersion(w, value.Version)
	adminhttp.WriteJSON(w, value)
	return nil
}

func (s *Store) saveDatasource(ctx context.Context, input DatasourceInput, actor, id string, version int64) (Datasource, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return Datasource{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Datasource{}, err
	}
	defer tx.Rollback()
	if id == "" {
		id = rand.Text()
		scope, _ := json.Marshal(input.Scope)
		_, err = tx.ExecContext(ctx, "INSERT INTO diagnostic_datasource(id,scope,document,enabled,version) VALUES(?,?,?,?,1)",
			id, scope, raw, input.Enabled)
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE diagnostic_datasource SET document=?,enabled=?,version=version+1,
			health='untested',checked_at=NULL,message='' WHERE id=? AND version=?`, raw, input.Enabled, id, version)
		if err != nil {
			return Datasource{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return Datasource{}, err
		}
		if n == 0 {
			return Datasource{}, ErrVersion
		}
	}
	if err != nil {
		return Datasource{}, err
	}
	if s.audit != nil {
		if err = s.audit(ctx, tx, actor, "datasource_saved", id); err != nil {
			return Datasource{}, err
		}
	}
	out, err := datasourceByID(ctx, tx, id)
	if err != nil {
		return Datasource{}, err
	}
	return out.Datasource, tx.Commit()
}

func (s *Server) testDatasource(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	d, err := s.getDatasource(ctx, p, r.PathValue("datasourceId"), ConnectorTest)
	if err != nil {
		return err
	}
	if !d.Enabled || s.Assets == nil {
		return ErrConflict
	}
	v, err := requestedVersion(r)
	if err != nil {
		return err
	}
	if v != d.Version {
		return ErrVersion
	}
	query, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	assets, err := s.Assets.Assets(query, d.Config, d.credential, scopeKey(d.Scope))
	state, message := connectorStatus(assets, len(d.Config.AssetIDs), err)
	if err = s.require(ctx, p, ConnectorTest, d.Scope, false); err != nil {
		return err
	}
	out, err := s.Store.syncDatasource(ctx, d, p.Subject, state, message, assets)
	if err != nil {
		return err
	}
	setVersion(w, out.Version)
	adminhttp.WriteJSON(w, out)
	return nil
}

func connectorStatus(values []assetmeta.Asset, count int, err error) (string, string) {
	switch {
	case errors.Is(err, assetmeta.ErrCredentials):
		return "credentials_unavailable", "凭据引用不可用或与数据源范围不匹配。"
	case errors.Is(err, assetmeta.ErrConfiguration):
		return "configuration_error", "目标地址或资产配置无效。"
	case errors.Is(err, assetmeta.ErrAuthorization):
		return "unauthorized", "上游拒绝了当前凭据或资源范围。"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", "上游读取超时。"
	case err != nil:
		return "unavailable", "上游数据源不可用。"
	case len(values) < count:
		return "partial", "部分配置资产未返回。"
	default:
		return "available", "已读取配置资产。"
	}
}

func assetKey(d datasourceRecord, nativeID string) string {
	key := strings.Join([]string{d.ID, d.Config.Kind, d.Config.Region, d.Config.Endpoint, nativeID}, "\x00")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}

func (s *Store) syncDatasource(ctx context.Context, d datasourceRecord, actor, state, message string, assets []assetmeta.Asset) (Datasource, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Datasource{}, err
	}
	defer tx.Rollback()
	now := nowUTC().UnixMilli()
	var synced any
	if state == "available" || state == "partial" {
		synced = now
	}
	result, err := tx.ExecContext(ctx, `UPDATE diagnostic_datasource SET health=?,message=?,checked_at=?,version=version+1,
		synced_at=COALESCE(?,synced_at) WHERE id=? AND version=? AND enabled=1`, state, message, now, synced, d.ID, d.Version)
	if err != nil {
		return Datasource{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Datasource{}, err
	}
	if n == 0 {
		return Datasource{}, ErrVersion
	}
	if synced != nil {
		for _, a := range assets {
			if !slices.Contains(d.Config.AssetIDs, a.ExternalID) {
				continue
			}
			a.Name, a.State = cleanText(a.Name, 240), cleanText(a.State, 64)
			if a.Name == "" {
				a.Name = a.ExternalID
			}
			value, err := json.Marshal(a)
			if err != nil {
				return Datasource{}, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO diagnostic_asset VALUES(?,?,?,?)
				ON CONFLICT(id) DO UPDATE SET document=excluded.document,observed_at=excluded.observed_at`,
				assetKey(d, a.ExternalID), d.ID, value, now); err != nil {
				return Datasource{}, err
			}
		}
	}
	if s.audit != nil {
		if err = s.audit(ctx, tx, actor, "datasource_checked", d.ID); err != nil {
			return Datasource{}, err
		}
	}
	out, err := datasourceByID(ctx, tx, d.ID)
	if err != nil {
		return Datasource{}, err
	}
	return out.Datasource, tx.Commit()
}

func readInventory(row rowScanner) (inventoryAsset, error) {
	var a inventoryAsset
	var data []byte
	var observed int64
	err := row.Scan(&a.ID, &a.DatasourceID, &data, &observed)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.ObservedAt = time.UnixMilli(observed).UTC()
	return a, json.Unmarshal(data, &a.Asset)
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	d, err := s.getDatasource(ctx, p, r.PathValue("datasourceId"), ConnectorRead)
	if err != nil {
		return err
	}
	items := []inventoryAsset{}
	for _, native := range d.Config.AssetIDs {
		a, err := s.Store.asset(ctx, assetKey(d, native))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		items = append(items, a)
	}
	adminhttp.WriteJSON(w, map[string]any{"items": items})
	return nil
}

func (s *Store) asset(ctx context.Context, id string) (inventoryAsset, error) {
	return readInventory(s.db.QueryRowContext(ctx, "SELECT id,datasource_id,document,observed_at FROM diagnostic_asset WHERE id=?", id))
}

type IngressInput struct {
	IncidentID string    `json:"incidentId"`
	AssetID    string    `json:"assetId"`
	ExternalID string    `json:"externalId"`
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observedAt"`
	Summary    string    `json:"summary"`
}

func (s *Server) ingressSignal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	d, err := datasourceByID(ctx, s.Store.db, r.PathValue("connectorId"))
	credentials := r.Header.Values("Authorization")
	denied := httpError{401, "CONNECTOR_AUTHENTICATION_REQUIRED", "需要有效的采集连接器凭据。"}
	if err != nil || !d.Enabled || d.ingress == "" || s.Assets == nil || len(credentials) != 1 {
		writeError(w, denied)
		return
	}
	key, err := s.Assets.IngressToken(d.ingress, scopeKey(d.Scope), d.Config)
	if err != nil || subtle.ConstantTimeCompare([]byte(credentials[0]), []byte("Bearer "+key)) != 1 {
		writeError(w, denied)
		return
	}
	if s.IngressScopeAllowed != nil {
		allowed, err := s.IngressScopeAllowed(ctx, d.Scope)
		if err != nil {
			writeFailure(w, err)
			return
		}
		if !allowed {
			writeFailure(w, ErrForbidden)
			return
		}
	}
	var input IngressInput
	now := nowUTC()
	if !adminhttp.Decode(w, r, &input) || !validID(input.IncidentID) || !validID(input.ExternalID) ||
		!slices.Contains(d.Config.AssetIDs, input.AssetID) || input.ObservedAt.Before(now.Add(-24*time.Hour)) ||
		input.ObservedAt.After(now.Add(time.Minute)) || len(input.Summary) > 2000 ||
		(input.State != "firing" && input.State != "resolved" && input.State != "observed") {
		writeFailure(w, badInput)
		return
	}
	i, err := s.Store.Get(ctx, input.IncidentID)
	if err != nil || i.Scope != d.Scope {
		writeFailure(w, ErrNotFound)
		return
	}
	value := correlation.CorrelationSignal{ID: input.ExternalID, Source: correlation.CorrelationSource(d.Config.Kind), Type: correlation.SignalTypeAlert,
		FirstSeen: input.ObservedAt.UTC().Format(time.RFC3339Nano), LastSeen: input.ObservedAt.UTC().Format(time.RFC3339Nano),
		Summary: cleanText(input.Summary, 2000)}
	signal := Signal{Key: d.ID + "/" + input.AssetID + "/" + input.ExternalID, State: input.State, First: input.ObservedAt, Last: input.ObservedAt, Value: value}
	if err = s.Store.acceptIngress(ctx, d, i, assetKey(d, input.AssetID), signal, now); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Store) acceptIngress(ctx context.Context, d datasourceRecord, i Incident, assetID string, v Signal, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	i, err = lockedIncident(ctx, tx, i.ID, i.Version)
	if err != nil {
		return err
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM diagnostic_datasource d
		JOIN diagnostic_asset a ON a.datasource_id=d.id JOIN diagnostic_dependency l ON l.target_id=a.id
		WHERE d.id=? AND d.version=? AND d.enabled=1 AND a.id=? AND l.target_kind='asset' AND l.source_id=?)`,
		d.ID, d.Version, assetID, i.ID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	if i.Status == Closed {
		return ErrConflict
	}
	physicalAsset := i
	physicalAsset.ResourceID = assetID
	changed, err := acceptSignal(ctx, tx, physicalAsset, v, now)
	if err != nil || !changed {
		return err
	}
	i.UpdatedAt = now
	if v.First.Before(i.FirstSeen) {
		i.FirstSeen = v.First
	}
	if v.Last.After(i.LastSeen) {
		i.LastSeen = v.Last
	}
	if err = persist(ctx, tx, &i); err != nil {
		return err
	}
	if err = s.record(ctx, tx, i, "connector:"+d.ID, "signal_received", "", "", ""); err != nil {
		return err
	}
	return tx.Commit()
}

type assetIncidentAnchor struct {
	ID, ConnectorID string
	Version         int64
}

func (s *Server) resolveAssetIncident(ctx context.Context, p identity.Principal, input CreateRequest) (Incident, *assetIncidentAnchor, error) {
	scope, err := s.canonicalScope(ctx, p, CreatePermission, input.Scope)
	if err != nil {
		return Incident{}, nil, err
	}
	a, err := s.Store.asset(ctx, input.AssetID)
	if err != nil {
		return Incident{}, nil, err
	}
	d, err := s.getDatasource(ctx, p, a.DatasourceID, ConnectorRead)
	if err != nil {
		return Incident{}, nil, err
	}
	if !d.Enabled || scope != d.Scope || assetKey(d, a.ExternalID) != a.ID || !slices.Contains(d.Config.AssetIDs, a.ExternalID) {
		return Incident{}, nil, ErrNotFound
	}
	i := Incident{Scope: scope, ResourceID: a.ID, Resource: finding.ResourceRef{Kind: a.Kind, Name: a.Name, Namespace: scope.Namespace}}
	return i, &assetIncidentAnchor{a.ID, d.ID, d.Version}, nil
}
