package incident

import (
	"context"
	"encoding/json"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

const (
	RCARead        = "diagnosis.rca.read"
	RCARun         = "diagnosis.rca.run"
	RCACancel      = "diagnosis.rca.cancel"
	RCASave        = "diagnosis.rca.save"
	maxRCAEvidence = 12
)

type RCAInput struct {
	Observation string   `json:"observation,omitempty"`
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

type RCAConclusion struct {
	State      string   `json:"state"`
	Judgment   string   `json:"judgment"`
	References []string `json:"references"`
	Gaps       string   `json:"gaps"`
	Next       string   `json:"next"`
}

type RCARevision struct {
	Version    int64         `json:"version"`
	Actor      string        `json:"actor"`
	At         time.Time     `json:"at"`
	Conclusion RCAConclusion `json:"conclusion"`
}

type RCATask struct {
	ID            string        `json:"id"`
	IncidentID    string        `json:"incidentId"`
	State         string        `json:"state"`
	Observation   string        `json:"observation,omitempty"`
	Evidence      []Evidence    `json:"evidence"`
	Actor         string        `json:"actor"`
	Version       int64         `json:"version"`
	CreatedAt     time.Time     `json:"createdAt"`
	StartedAt     *time.Time    `json:"startedAt,omitempty"`
	FinishedAt    *time.Time    `json:"finishedAt,omitempty"`
	ErrorCode     string        `json:"errorCode,omitempty"`
	Revisions     []RCARevision `json:"revisions"`
	PolicyID      string        `json:"policyId,omitempty"`
	PolicyVersion int64         `json:"policyVersion,omitempty"`
}

type rcaEvidence struct {
	Evidence
	Historical bool            `json:"historical"`
	Signal     json.RawMessage `json:"signal"`
}

type rcaJob struct {
	RCATask
	Snapshots    []rcaEvidence
	ActorContext taskActor
	Lease        string
}

type taskActor struct {
	identity.Principal
	Generation string
	Admin      bool
}

func actorFor(p identity.Principal) taskActor {
	return taskActor{p, p.SessionVersion, p.Administrator}
}

func (a taskActor) principal() identity.Principal {
	p := a.Principal
	p.SessionVersion, p.Administrator = a.Generation, a.Admin
	return p
}

type IncidentModel func(context.Context, Incident, string, []byte) (string, error)
