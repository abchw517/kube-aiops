package incident

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func requestFilter(r *http.Request) (Filter, error) {
	q := r.URL.Query()
	f := Filter{
		Environment: q.Get("environmentId"), Business: q.Get("businessId"), Cluster: q.Get("clusterId"),
		Namespace: q.Get("namespace"), Status: q.Get("status"), Severity: q.Get("severity"),
		Source: q.Get("source"), Search: strings.TrimSpace(q.Get("q")), Platform: q.Get("platform"),
	}
	for _, value := range []string{f.Environment, f.Business, f.Cluster, f.Namespace, f.Platform} {
		if value != "" && !validID(value) {
			return Filter{}, badInput
		}
	}
	if len(f.Search) > 256 || f.Status != "" && !validStatus(f.Status) || f.Severity != "" && !validSeverity(f.Severity) {
		return Filter{}, badInput
	}
	switch f.Source {
	case "", "manual", "kubernetes-events", "prometheus", "alertmanager", "victorialogs", "k8sgpt", "zabbix", "aliyun-ecs", "tencent-cvm":
	default:
		return Filter{}, badInput
	}
	for name, out := range map[string]*time.Time{"from": &f.From, "to": &f.To} {
		if value := q.Get(name); value != "" {
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil || t.Year() < 1970 || t.Year() > 9999 {
				return Filter{}, badInput
			}
			*out = t.UTC()
		}
	}
	if !f.From.IsZero() && !f.To.IsZero() && f.From.After(f.To) {
		return Filter{}, badInput
	}
	return f, nil
}

func requestPage(r *http.Request, key string) (cursor, int, error) {
	q := r.URL.Query()
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 100 {
			return cursor{}, 0, badInput
		}
		limit = v
	}
	after, ok := decodeCursor(q.Get("continue"), key)
	if !ok {
		return cursor{}, 0, badInput
	}
	return after, limit, nil
}

func (s *Server) collection(r *http.Request, p identity.Principal, f Filter) (func(Incident) (bool, error), error) {
	ctx := r.Context()
	if err := s.require(ctx, p, ListPermission, Scope{}, true); err != nil {
		return nil, err
	}
	if f.Cluster != "" {
		scope := Scope{EnvironmentID: f.Environment, BusinessID: f.Business, ClusterID: f.Cluster, Namespace: f.Namespace}
		if err := s.require(ctx, p, ListPermission, scope, true); err != nil {
			return nil, err
		}
	}
	visible := map[Scope]bool{}
	return func(i Incident) (bool, error) {
		allowed, cached := visible[i.Scope]
		if cached {
			return allowed, nil
		}
		allowed, err := s.Authorize(ctx, p, ListPermission, i.Scope, false)
		if err != nil {
			return false, err
		}
		if allowed && f.Source != "" && f.Source != "manual" {
			allowed, err = s.Authorize(ctx, p, EvidencePermission, i.Scope, false)
			if err != nil {
				return false, err
			}
		}
		visible[i.Scope] = allowed
		return allowed, nil
	}, nil
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	f, err := requestFilter(r)
	if err != nil {
		return err
	}
	key := f.key()
	after, limit, err := requestPage(r, key)
	if err != nil {
		return err
	}
	visible, err := s.collection(r, p, f)
	if err != nil {
		return err
	}
	out := struct {
		Page[Incident]
		Coverage string `json:"coverage"`
	}{Page[Incident]{Items: []Incident{}}, "complete"}
	err = s.Store.visit(r.Context(), f, after, func(i Incident) (bool, error) {
		ok, err := visible(i)
		if !ok || err != nil {
			return true, err
		}
		if len(out.Items) == limit {
			last := out.Items[limit-1]
			out.Pagination.Continue = encodeCursor(cursor{Time: last.LastSeen.UnixMilli(), ID: last.ID, From: key})
			return false, nil
		}
		i.Sources = nil
		out.Items = append(out.Items, i)
		return true, nil
	})
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, out)
	return nil
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	f, err := requestFilter(r)
	if err != nil {
		return err
	}
	visible, err := s.collection(r, p, f)
	if err != nil {
		return err
	}
	out := Overview{Counts: map[string]int{"total": 0, "active": 0, Pending: 0, Investigating: 0, Recovered: 0, Closed: 0},
		Trend: map[string]int{}, ByPlatform: map[string]int{}, Coverage: "complete"}
	trendStart := nowUTC().Truncate(24*time.Hour).AddDate(0, 0, -6)
	for day := 0; day < 7; day++ {
		out.Trend[trendStart.AddDate(0, 0, day).Format(time.DateOnly)] = 0
	}
	err = s.Store.visit(r.Context(), f, cursor{}, func(i Incident) (bool, error) {
		ok, err := visible(i)
		if !ok || err != nil {
			return true, err
		}
		out.Counts["total"]++
		out.Counts[i.Status]++
		if i.Status == Pending || i.Status == Investigating {
			out.Counts["active"]++
		}
		if key := i.FirstSeen.Format(time.DateOnly); i.FirstSeen.After(trendStart) || i.FirstSeen.Equal(trendStart) {
			if _, present := out.Trend[key]; present {
				out.Trend[key]++
			}
		}
		out.ByPlatform[incidentPlatform(i)]++
		return true, nil
	})
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, out)
	return nil
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.visible(ctx, p, r.PathValue("id"), ReadPermission)
	if err != nil {
		return err
	}
	actions := Actions{RCA: s.RCAAccess(ctx, p, i)}
	actions.Topology, err = s.Authorize(ctx, p, TopologyRead, i.Scope, false)
	if err != nil {
		return err
	}
	if actions.Topology {
		actions.TopologyWrite, err = s.Authorize(ctx, p, TopologyWrite, i.Scope, false)
		if err != nil {
			return err
		}
	}
	for code, dest := range map[string]*bool{UpdatePermission: &actions.Update, CorrelatePermission: &actions.Correlate, CreatePermission: &actions.Split, EvidencePermission: &actions.Evidence} {
		*dest, err = s.Authorize(ctx, p, code, i.Scope, false)
		if err != nil {
			return err
		}
	}
	actions.Correlate = actions.Correlate && actions.Evidence && i.Status != Closed
	actions.Update = actions.Update && i.Status != Closed
	actions.Split = actions.Split && actions.Correlate
	out := Detail{Actions: actions, Version: i.Version, Coverage: "unavailable"}
	if actions.Correlate {
		out.Candidates, err = s.Store.candidates(ctx, i)
		if err != nil {
			return err
		}
	}
	if actions.Evidence {
		out.Counts, err = s.Store.evidenceCounts(ctx, i.ID)
		if err != nil {
			return err
		}
		out.Coverage = coverage(i.Sources)
	} else {
		i.Sources = nil
	}
	out.Incident = i
	responseVersion(w, i)
	adminhttp.WriteJSON(w, out)
	return nil
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	i, err := s.visible(r.Context(), p, r.PathValue("id"), ReadPermission)
	if err != nil {
		return err
	}
	after, limit, err := requestPage(r, "timeline:"+i.ID)
	if err != nil {
		return err
	}
	if after.ID != "" {
		if n, err := strconv.ParseInt(after.ID, 10, 64); err != nil || n < 1 {
			return badInput
		}
	}
	page, err := s.Store.Timeline(r.Context(), i.ID, after, limit)
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, page)
	return nil
}

func (s *Server) evidence(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	i, err := s.visible(r.Context(), p, r.PathValue("id"), EvidencePermission)
	if err != nil {
		return err
	}
	after, limit, err := requestPage(r, "evidence:"+i.ID)
	if err != nil {
		return err
	}
	page, err := s.Store.Evidence(r.Context(), i.ID, after, limit)
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, page)
	return nil
}

func (s *Server) evidenceDetail(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	i, err := s.visible(r.Context(), p, r.PathValue("id"), EvidencePermission)
	if errors.Is(err, ErrForbidden) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	value, err := s.Store.EvidenceDetail(r.Context(), i.ID, r.PathValue("evidenceId"))
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, value)
	return nil
}

func coverage(sources []correlation.SourceStatus) string {
	usable, incomplete := false, false
	for _, source := range sources {
		switch source.State {
		case correlation.SourceAvailable:
			usable = true
		case correlation.SourcePartial:
			usable, incomplete = true, true
		case correlation.SourceUnavailable:
			incomplete = true
		}
	}
	if !usable {
		return "unavailable"
	}
	if incomplete {
		return "partial"
	}
	return "complete"
}
