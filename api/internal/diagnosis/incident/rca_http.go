package incident

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func (s *Server) registerRCA(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/diagnostics/incidents/{id}/rca-runs", s.protect(s.startRCA))
	mux.HandleFunc("GET /api/v1/diagnostics/incidents/{id}/rca-runs", s.protect(s.listRCA))
	mux.HandleFunc("GET /api/v1/diagnostics/incidents/{id}/rca-runs/{runId}", s.protect(s.getRCA))
	mux.HandleFunc("POST /api/v1/diagnostics/incidents/{id}/rca-runs/{runId}/cancel", s.protect(s.stopRCA))
	mux.HandleFunc("PATCH /api/v1/diagnostics/incidents/{id}/rca-runs/{runId}/conclusion", s.protect(s.saveRCA))
}

func (s *Server) rcaIncident(ctx context.Context, p identity.Principal, id, code string) (Incident, error) {
	i, err := s.visible(ctx, p, id, code)
	if err != nil {
		return i, err
	}
	return i, s.require(ctx, p, EvidencePermission, i.Scope, false)
}

func (s *Server) startRCA(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.rcaIncident(ctx, p, r.PathValue("id"), RCARun)
	if err != nil {
		return err
	}
	if s.Model == nil || s.ModelAvailable != nil && !s.ModelAvailable(i) {
		return httpError{503, "RCA_MODEL_UNAVAILABLE", "当前实例暂未配置模型。"}
	}
	var input RCAInput
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !validID(keys[0]) || len(keys[0]) > 128 ||
		!adminhttp.Decode(w, r, &input) || len(input.Observation) > 4000 || len(input.EvidenceIDs) > maxRCAEvidence {
		return badInput
	}
	for _, id := range input.EvidenceIDs {
		if !validID(id) {
			return badInput
		}
	}
	slices.Sort(input.EvidenceIDs)
	input.EvidenceIDs = slices.Compact(input.EvidenceIDs)
	data, _ := json.Marshal(input)
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	input.Observation = cleanText(input.Observation, 4000)
	task, err := s.Store.queueRun(ctx, i, actorFor(p), keys[0], digest, input, nil)
	if err != nil {
		return err
	}
	w.Header().Set("Location", base+"/incidents/"+i.ID+"/rca-runs/"+task.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	adminhttp.WriteJSON(w, task)
	return nil
}

func (s *Server) RCAAccess(ctx context.Context, p identity.Principal, i Incident) map[string]bool {
	caps := map[string]bool{}
	ev, err := s.Authorize(ctx, p, EvidencePermission, i.Scope, false)
	if err != nil || !ev {
		return caps
	}
	for key, code := range map[string]string{"read": RCARead, "run": RCARun, "cancel": RCACancel, "save": RCASave} {
		ok, err := s.Authorize(ctx, p, code, i.Scope, false)
		caps[key] = err == nil && ok
	}
	if s.Model == nil || s.ModelAvailable != nil && !s.ModelAvailable(i) {
		caps["run"] = false
	}
	return caps
}

func (s *Server) listRCA(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.rcaIncident(ctx, p, r.PathValue("id"), RCARead)
	if err != nil {
		return err
	}
	key := "rca:" + i.ID
	after, limit, err := requestPage(r, key)
	if err != nil {
		return err
	}
	if limit > 50 {
		limit = 50
	}
	query := "SELECT " + rcaColumns + " FROM diagnostic_rca_run WHERE incident_id=?"
	args := []any{i.ID}
	if after.ID != "" {
		query += " AND (created_at,id)<(?,?)"
		args = append(args, after.Time, after.ID)
	}
	rows, err := s.Store.db.QueryContext(ctx, query+" ORDER BY created_at DESC,id DESC LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	page := Page[RCATask]{Items: []RCATask{}}
	for rows.Next() {
		j, err := readJob(rows)
		if err != nil {
			return err
		}
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.Pagination.Continue = encodeCursor(cursor{ID: last.ID, Time: last.CreatedAt.UnixMilli(), From: key})
			break
		}
		// The task detail rechecks references before returning any evidence or observation.
		j.Observation = ""
		j.Evidence = nil
		page.Items = append(page.Items, j.RCATask)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for n := range page.Items {
		page.Items[n].Evidence = []Evidence{}
	}
	adminhttp.WriteJSON(w, page)
	return nil
}

func (s *Server) rcaRecord(r *http.Request, p identity.Principal, code string) (rcaJob, error) {
	ctx := r.Context()
	i, err := s.rcaIncident(ctx, p, r.PathValue("id"), code)
	if err != nil {
		return rcaJob{}, err
	}
	return readRun(ctx, s.Store.db, i.ID, r.PathValue("runId"))
}

func (s *Server) getRCA(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	j, err := s.rcaRecord(r, p, RCARead)
	if err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := s.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err = readRun(ctx, tx, j.IncidentID, j.ID)
	if err != nil {
		return err
	}
	switch err := evidenceCurrent(ctx, tx, j); {
	case errors.Is(err, ErrNotFound):
		j.ErrorCode = "evidence_changed"
		j.Observation = ""
		j.Evidence = []Evidence{}
	case err != nil:
		return err
	default:
		j.Revisions, err = runRevisions(ctx, tx, j.ID)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(j.Version, 10)))
	adminhttp.WriteJSON(w, j.RCATask)
	return nil
}

func (s *Server) stopRCA(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	j, err := s.rcaRecord(r, p, RCACancel)
	if err != nil {
		return err
	}
	if err = s.Store.cancelRun(r.Context(), j, p.Subject); err != nil {
		return err
	}
	if cancel, ok := s.activeRCA.Load(j.ID); ok {
		cancel.(context.CancelFunc)()
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) saveRCA(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	j, err := s.rcaRecord(r, p, RCASave)
	if err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	if j.State != "succeeded" {
		return ErrConflict
	}
	var input RCAConclusion
	if !adminhttp.Decode(w, r, &input) || len(input.References) > maxRCAEvidence {
		return badInput
	}
	result, ok := validateConclusion(input, j.Snapshots, nowUTC())
	if !ok {
		return badInput
	}
	if err = s.Store.amendRun(r.Context(), j, p.Subject, version, result); err != nil {
		return err
	}
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(version+1, 10)))
	adminhttp.WriteJSON(w, RCARevision{Version: version + 1, Actor: p.Subject, At: nowUTC(), Conclusion: result})
	return nil
}

func validateConclusion(input RCAConclusion, snapshots []rcaEvidence, now time.Time) (RCAConclusion, bool) {
	if input.State != "supported" && input.State != "insufficient" && input.State != "conflicting" {
		return RCAConclusion{}, false
	}
	for _, p := range []*string{&input.Judgment, &input.Gaps, &input.Next} {
		*p = cleanText(*p, 1600)
		if *p == "" {
			return RCAConclusion{}, false
		}
	}
	valid := make(map[string]rcaEvidence, len(snapshots))
	for _, v := range snapshots {
		valid[v.ID] = v
	}
	ids := []string{}
	current := false
	for _, id := range input.References {
		v, ok := valid[id]
		if !ok || slices.Contains(ids, id) {
			continue
		}
		ids = append(ids, id)
		current = current || (!v.Historical && !v.ObservedAt.After(now) && now.Sub(v.ObservedAt) <= 30*time.Minute)
	}
	input.References = ids
	if !current && input.State != "insufficient" {
		input.State = "insufficient"
		input.Judgment = "引用中缺少能支持当前判断的现场证据。"
	}
	return input, true
}
