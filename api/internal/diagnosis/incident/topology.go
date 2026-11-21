package incident

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const (
	TopologyRead  = "diagnosis.topology.read"
	TopologyWrite = "diagnosis.topology.write"
)

type TopologyNode struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Scope      Scope  `json:"scope"`
	ResourceID string `json:"resourceId"`
	IncidentID string `json:"incidentId,omitempty"`
	State      string `json:"state,omitempty"`
	Platform   string `json:"platform"`
}

type TopologyLink struct {
	Source     string    `json:"source"`
	Target     string    `json:"target"`
	Relation   string    `json:"relation"`
	Confidence string    `json:"confidence"`
	Reason     string    `json:"reason"`
	Actor      string    `json:"actor"`
	RecordedAt time.Time `json:"recordedAt"`
	EvidenceID string    `json:"evidenceId,omitempty"`
}

type Topology struct {
	Root      string         `json:"root"`
	Nodes     []TopologyNode `json:"nodes"`
	Edges     []TopologyLink `json:"edges"`
	Coverage  string         `json:"coverage"`
	Truncated bool           `json:"truncated"`
}

type TopologyInput struct {
	TargetIncidentID string `json:"targetIncidentId,omitempty"`
	TargetAssetID    string `json:"targetAssetId,omitempty"`
	Relation         string `json:"relation"`
	Confidence       string `json:"confidence,omitempty"`
	Reason           string `json:"reason"`
	EvidenceID       string `json:"evidenceId,omitempty"`
	Remove           bool   `json:"remove,omitempty"`
}

func (s *Server) registerTopology(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/diagnostics/incidents/{id}/topology", s.protect(s.topology))
	mux.HandleFunc("POST /api/v1/diagnostics/incidents/{id}/topology/links", s.protect(s.saveTopologyLink))
}

func nodeOf(i Incident) TopologyNode {
	name := i.Resource.Name
	if name == "" {
		name = i.Title
	}
	return TopologyNode{ID: "incident:" + i.ID, Name: name, Kind: i.Resource.Kind, Scope: i.Scope, ResourceID: i.ResourceID,
		IncidentID: i.ID, State: i.Status, Platform: incidentPlatform(i)}
}

func (s *Server) graphNode(ctx context.Context, p identity.Principal, key string) (TopologyNode, error) {
	kind, id, _ := strings.Cut(key, ":")
	switch kind {
	case "incident":
		i, err := s.visible(ctx, p, id, TopologyRead)
		return nodeOf(i), err
	case "asset":
		a, err := s.Store.asset(ctx, id)
		if err != nil {
			return TopologyNode{}, err
		}
		d, err := s.getDatasource(ctx, p, a.DatasourceID, ConnectorRead)
		if err != nil {
			return TopologyNode{}, err
		}
		if !slices.Contains(d.Config.AssetIDs, a.ExternalID) || assetKey(d, a.ExternalID) != a.ID {
			return TopologyNode{}, ErrNotFound
		}
		return TopologyNode{ID: key, Name: a.Name, Kind: a.Kind, Scope: d.Scope, ResourceID: a.ID, State: a.State, Platform: d.Config.Kind}, nil
	default:
		return TopologyNode{}, ErrNotFound
	}
}

func (s *Server) topology(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	i, err := s.visible(r.Context(), p, r.PathValue("id"), TopologyRead)
	if err != nil {
		return err
	}
	depth := 2
	if raw := r.URL.Query().Get("depth"); raw != "" {
		depth, err = strconv.Atoi(raw)
		if err != nil || depth < 0 || depth > 4 {
			return badInput
		}
	}
	graph, err := s.walkTopology(r.Context(), p, i, depth)
	if err != nil {
		return err
	}
	adminhttp.WriteJSON(w, graph)
	return nil
}

func (s *Store) neighbors(ctx context.Context, node string) ([]TopologyLink, error) {
	kind, id, _ := strings.Cut(node, ":")
	rows, err := s.db.QueryContext(ctx, `SELECT source_id,target_kind,target_id,relation,confidence,reason,actor,recorded_at,evidence_id
		FROM diagnostic_dependency WHERE (target_kind=? AND target_id=?) OR (?='incident' AND source_id=?)
		ORDER BY recorded_at DESC,source_id,target_kind,target_id,relation LIMIT 128`, kind, id, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []TopologyLink
	for rows.Next() {
		var e TopologyLink
		var src, targetKind, targetID string
		var at int64
		if err := rows.Scan(&src, &targetKind, &targetID, &e.Relation, &e.Confidence, &e.Reason, &e.Actor, &at, &e.EvidenceID); err != nil {
			return nil, err
		}
		e.Source, e.Target = "incident:"+src, targetKind+":"+targetID
		e.RecordedAt = time.UnixMilli(at).UTC()
		links = append(links, e)
	}
	return links, rows.Err()
}

func (s *Server) walkTopology(ctx context.Context, p identity.Principal, i Incident, depth int) (Topology, error) {
	root := nodeOf(i)
	out := Topology{Root: root.ID, Nodes: []TopologyNode{root}, Edges: []TopologyLink{}, Coverage: "unavailable"}
	nodes := map[string]TopologyNode{root.ID: root}
	denied := map[string]bool{}
	edges := map[[3]string]bool{}
	type entry struct {
		id    string
		level int
	}
	queue := []entry{{root.ID, 0}}
	for at := 0; at < len(queue); at++ {
		q := queue[at]
		if q.level >= depth {
			continue
		}
		links, err := s.Store.neighbors(ctx, q.id)
		if err != nil {
			return Topology{}, err
		}
		for _, edge := range links {
			other := edge.Target
			if other == q.id {
				other = edge.Source
			}
			if denied[other] {
				continue
			}
			node, found := nodes[other]
			if !found {
				node, err = s.graphNode(ctx, p, other)
				if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
					denied[other] = true
					continue
				}
				if err != nil {
					return Topology{}, err
				}
				if node.Scope.EnvironmentID != i.Scope.EnvironmentID {
					continue
				}
			}
			if edge.EvidenceID != "" {
				owner := strings.TrimPrefix(edge.Source, "incident:")
				anchor, err := s.rcaIncident(ctx, p, owner, TopologyRead)
				if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
					continue
				}
				if err != nil {
					return Topology{}, err
				}
				if _, err = s.Store.EvidenceDetail(ctx, anchor.ID, edge.EvidenceID); errors.Is(err, ErrNotFound) {
					continue
				} else if err != nil {
					return Topology{}, err
				}
			}
			key := [3]string{edge.Source, edge.Target, edge.Relation}
			if edges[key] {
				continue
			}
			if !found && len(nodes) >= 48 || len(out.Edges) >= 96 {
				out.Truncated = true
				continue
			}
			if !found {
				nodes[other] = node
				out.Nodes = append(out.Nodes, node)
				queue = append(queue, entry{other, q.level + 1})
			}
			edges[key] = true
			out.Edges = append(out.Edges, edge)
		}
	}
	if len(out.Edges) > 0 {
		out.Coverage = "partial"
	}
	return out, nil
}

func (s *Server) saveTopologyLink(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.visible(ctx, p, r.PathValue("id"), TopologyWrite)
	if err != nil {
		return err
	}
	var in TopologyInput
	if !adminhttp.Decode(w, r, &in) || (in.TargetAssetID == "") == (in.TargetIncidentID == "") ||
		!slices.Contains([]string{"calls", "depends_on", "runs_on", "monitored_by"}, in.Relation) || !validReason(in.Reason) {
		return badInput
	}
	kind, id := "incident", in.TargetIncidentID
	if in.TargetAssetID != "" {
		kind, id = "asset", in.TargetAssetID
	}
	if !validID(id) || kind == "incident" && id == i.ID {
		return badInput
	}
	node, err := s.graphNode(ctx, p, kind+":"+id)
	if err != nil {
		return err
	}
	if node.Scope.EnvironmentID != i.Scope.EnvironmentID || kind == "asset" && node.Scope != i.Scope {
		return ErrBadScope
	}
	if kind == "incident" {
		if err = s.require(ctx, p, TopologyWrite, node.Scope, false); err != nil {
			return err
		}
	}
	if !in.Remove {
		if in.Confidence != "confirmed" && in.Confidence != "inferred" || in.Confidence == "inferred" && in.EvidenceID == "" {
			return badInput
		}
		if in.EvidenceID != "" {
			if err = s.require(ctx, p, EvidencePermission, i.Scope, false); err != nil {
				return err
			}
			if _, err = s.Store.EvidenceDetail(ctx, i.ID, in.EvidenceID); err != nil {
				return err
			}
		}
	}
	tx, err := s.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowUTC()
	if in.Remove {
		_, err = tx.ExecContext(ctx, "DELETE FROM diagnostic_dependency WHERE source_id=? AND target_kind=? AND target_id=? AND relation=?",
			i.ID, kind, id, in.Relation)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO diagnostic_dependency VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(source_id,target_kind,target_id,relation) DO UPDATE SET confidence=excluded.confidence,
			reason=excluded.reason,actor=excluded.actor,recorded_at=excluded.recorded_at,evidence_id=excluded.evidence_id`,
			i.ID, kind, id, in.Relation, in.Confidence, cleanText(in.Reason, 1000), p.Subject, now.UnixMilli(), in.EvidenceID)
	}
	if err != nil {
		return err
	}
	i.UpdatedAt = now
	if err = s.Store.record(ctx, tx, i, p.Subject, "topology_changed", cleanText(in.Reason, 1000), "", ""); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
