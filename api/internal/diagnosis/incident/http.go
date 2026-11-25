package incident

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const base = "/api/v1/diagnostics"

type Server struct {
	Zabbix              *ZabbixIntegration
	Observability       *ObservabilityIntegration
	IngressScopeAllowed func(context.Context, Scope) (bool, error)
	Assets              AssetConnector
	ResolveScope        func(context.Context, Scope) (Scope, error)
	AutomationEnabled   func(Scope) bool

	Model          IncidentModel
	ModelAvailable func(Incident) bool
	ValidateActor  func(context.Context, identity.Principal) error
	activeRCA      sync.Map

	Store        *Store
	Origin       string
	Authenticate func(context.Context, *http.Request) (identity.Principal, error)
	Authorize    func(context.Context, identity.Principal, string, Scope, bool) (bool, error)
	Resolve      func(context.Context, identity.Principal, CreateRequest) (Incident, error)
	Collect      func(context.Context, Incident) (correlation.CorrelationBundle, error)
	ActiveGroup  func(context.Context, string) (bool, error)
}

type AssetConnector interface {
	Validate(assetmeta.Config) error
	Assets(context.Context, assetmeta.Config, string, string) ([]assetmeta.Asset, error)
	IngressToken(string, string, assetmeta.Config) (string, error)
}

type operation func(http.ResponseWriter, *http.Request, identity.Principal) error

type httpError struct {
	status  int
	code    string
	message string
}

func (e httpError) Error() string { return e.message }

var (
	badInput     = httpError{400, "INVALID_INCIDENT_REQUEST", "故障请求参数无效。"}
	needsVersion = httpError{428, "INCIDENT_VERSION_REQUIRED", "请提供当前故障版本。"}
	upstream     = httpError{503, "INCIDENT_SOURCES_UNAVAILABLE", "暂时无法读取关联数据源。"}
)

func (s *Server) Register(mux *http.ServeMux) {
	s.registerRCA(mux)
	s.registerTopology(mux)
	s.registerDatasource(mux)
	s.registerZabbix(mux)
	s.registerObservability(mux)
	s.registerPolicy(mux)
	mux.HandleFunc("GET /api/v1/diagnostics/overview", s.protect(s.overview))
	mux.HandleFunc("GET /api/v1/diagnostics/incidents", s.protect(s.list))
	mux.HandleFunc("POST /api/v1/diagnostics/incidents", s.protect(s.create))
	mux.HandleFunc("GET /api/v1/diagnostics/incidents/{id}", s.protect(s.detail))
	mux.HandleFunc("PATCH /api/v1/diagnostics/incidents/{id}", s.protect(s.update))
	mux.HandleFunc("GET /api/v1/diagnostics/incidents/{id}/timeline", s.protect(s.timeline))
	mux.HandleFunc("GET /api/v1/diagnostics/incidents/{id}/evidence", s.protect(s.evidence))
	mux.HandleFunc("GET /api/v1/diagnostics/incidents/{id}/evidence/{evidenceId}", s.protect(s.evidenceDetail))
	mux.HandleFunc("POST /api/v1/diagnostics/incidents/{id}/collect", s.protect(s.collect))
	mux.HandleFunc("POST /api/v1/diagnostics/incidents/{id}/merge", s.protect(s.merge))
	mux.HandleFunc("POST /api/v1/diagnostics/incidents/{id}/split", s.protect(s.split))
}

func (s *Server) protect(handle operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, err := s.Authenticate(r.Context(), r)
		if err != nil {
			writeError(w, httpError{401, "AUTHENTICATION_REQUIRED", "请登录后继续。"})
			return
		}
		if r.Method != http.MethodGet {
			origins := r.Header.Values("Origin")
			if len(origins) != 1 || origins[0] != s.Origin {
				writeFailure(w, ErrForbidden)
				return
			}
		}
		if err := handle(w, r, p); err != nil {
			writeFailure(w, err)
		}
	}
}

func writeFailure(w http.ResponseWriter, err error) {
	var known httpError
	switch {
	case errors.As(err, &known):
	case errors.Is(err, ErrForbidden):
		known = httpError{403, "INCIDENT_FORBIDDEN", "当前账号无权执行此操作。"}
	case errors.Is(err, ErrNotFound):
		known = httpError{404, "INCIDENT_NOT_FOUND", "故障或关联记录不存在。"}
	case errors.Is(err, ErrVersion):
		known = httpError{412, "INCIDENT_VERSION_MISMATCH", "故障已更新，请刷新后重试。"}
	case errors.Is(err, ErrConflict):
		known = httpError{409, "INCIDENT_CONFLICT", "当前故障状态、关联关系或重复请求与本次操作冲突。"}
	case errors.Is(err, ErrBadScope):
		known = httpError{400, "INCIDENT_SCOPE_INVALID", "请核对集群、命名空间、业务归属和资源。"}
	default:
		known = httpError{503, "INCIDENT_UNAVAILABLE", "故障服务暂不可用。"}
	}
	writeError(w, known)
}

func writeError(w http.ResponseWriter, e httpError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	adminhttp.WriteJSON(w, map[string]any{"error": map[string]string{"code": e.code, "message": e.message}})
}

func (s *Server) require(ctx context.Context, p identity.Principal, code string, scope Scope, collection bool) error {
	allowed, err := s.Authorize(ctx, p, code, scope, collection)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (s *Server) visible(ctx context.Context, p identity.Principal, id string, action string) (Incident, error) {
	if !validID(id) {
		return Incident{}, ErrNotFound
	}
	item, err := s.Store.Get(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	if err := s.require(ctx, p, ReadPermission, item.Scope, false); err != nil {
		if errors.Is(err, ErrForbidden) {
			return Incident{}, ErrNotFound
		}
		return Incident{}, err
	}
	if action != ReadPermission {
		if err := s.require(ctx, p, action, item.Scope, false); err != nil {
			return Incident{}, err
		}
	}
	return item, nil
}

func requestedVersion(r *http.Request) (int64, error) {
	value := r.Header.Values("If-Match")
	if len(value) == 0 {
		return 0, needsVersion
	}
	if len(value) != 1 || len(value[0]) < 3 || !strings.HasPrefix(value[0], "\"") || !strings.HasSuffix(value[0], "\"") {
		return 0, badInput
	}
	v, err := strconv.ParseInt(value[0][1:len(value[0])-1], 10, 64)
	if err != nil || v < 1 || strconv.Quote(strconv.FormatInt(v, 10)) != value[0] {
		return 0, badInput
	}
	return v, nil
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

func responseVersion(w http.ResponseWriter, item Incident) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Version, 10)))
}

func responseItem(w http.ResponseWriter, item Incident, status int) {
	item.Sources = nil
	responseVersion(w, item)
	if status == http.StatusCreated {
		w.Header().Set("Location", base+"/incidents/"+item.ID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	adminhttp.WriteJSON(w, item)
}
