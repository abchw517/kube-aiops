package incident

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
)

const (
	Pending       = "pending"
	Investigating = "investigating"
	Recovered     = "recovered"
	Closed        = "closed"

	ListPermission      = "diagnosis.incident.list"
	CreatePermission    = "diagnosis.incident.create"
	ReadPermission      = "diagnosis.incident.read"
	UpdatePermission    = "diagnosis.incident.update"
	CorrelatePermission = "diagnosis.incident.correlate"
	EvidencePermission  = "diagnosis.evidence.read"
)

var (
	ErrNotFound  = errors.New("incident not found")
	ErrVersion   = errors.New("incident version changed")
	ErrConflict  = errors.New("incident state conflicts with request")
	ErrBadScope  = errors.New("invalid incident scope")
	ErrForbidden = errors.New("incident access denied")
)

type Scope struct {
	EnvironmentID string `json:"environmentId"`
	BusinessID    string `json:"businessId,omitempty"`
	ClusterID     string `json:"clusterId"`
	Namespace     string `json:"namespace"`
}

type Incident struct {
	AnchorAt    time.Time                  `json:"-"`
	ID          string                     `json:"id"`
	DisplayKey  string                     `json:"displayKey"`
	Scope       Scope                      `json:"scope"`
	Title       string                     `json:"title"`
	Description string                     `json:"description,omitempty"`
	Resource    finding.ResourceRef        `json:"resource"`
	ResourceID  string                     `json:"resourceId"`
	FindingID   string                     `json:"findingId,omitempty"`
	Severity    string                     `json:"severity"`
	Status      string                     `json:"status"`
	OwnerGroup  string                     `json:"ownerGroupId,omitempty"`
	Source      string                     `json:"source"`
	Version     int64                      `json:"version"`
	FirstSeen   time.Time                  `json:"firstSeen"`
	LastSeen    time.Time                  `json:"lastSeen"`
	CreatedAt   time.Time                  `json:"createdAt"`
	UpdatedAt   time.Time                  `json:"updatedAt"`
	MergedInto  string                     `json:"mergedInto,omitempty"`
	Sources     []correlation.SourceStatus `json:"sources,omitempty"`
}

type CreateRequest struct {
	AssetID     string              `json:"assetId,omitempty"`
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Scope       Scope               `json:"scope"`
	Severity    string              `json:"severity"`
	FindingID   string              `json:"findingId,omitempty"`
	Resource    finding.ResourceRef `json:"resource,omitempty"`
	ResourceID  string              `json:"resourceId,omitempty"`
}

type UpdateRequest struct {
	Severity   string  `json:"severity,omitempty"`
	Status     string  `json:"status,omitempty"`
	OwnerGroup *string `json:"ownerGroupId,omitempty"`
	Reason     string  `json:"reason"`
}

type TimelineEntry struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Kind   string    `json:"kind"`
	Reason string    `json:"reason,omitempty"`
	Before string    `json:"before,omitempty"`
	After  string    `json:"after,omitempty"`
}

type Evidence struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Kind        string    `json:"kind"`
	ObservedAt  time.Time `json:"observedAt"`
	CollectedAt time.Time `json:"collectedAt"`
	Summary     string    `json:"summary,omitempty"`
	State       string    `json:"state"`
}

type EvidenceDetail struct {
	Evidence
	Signal correlation.CorrelationSignal `json:"signal"`
}

type Signal struct {
	Key   string
	State string
	First time.Time
	Last  time.Time
	Value correlation.CorrelationSignal
}

type Actions struct {
	RCA           map[string]bool `json:"rca,omitempty"`
	Topology      bool            `json:"topology,omitempty"`
	TopologyWrite bool            `json:"topologyWrite,omitempty"`
	Update        bool            `json:"update"`
	Correlate     bool            `json:"correlate"`
	Split         bool            `json:"split"`
	Evidence      bool            `json:"evidence"`
}

type Detail struct {
	Incident   Incident       `json:"incident"`
	Actions    Actions        `json:"actions"`
	Version    int64          `json:"version"`
	Counts     map[string]int `json:"counts,omitempty"`
	Coverage   string         `json:"coverage"`
	Candidates []Incident     `json:"candidates,omitempty"`
}

type MergeRequest struct {
	SourceID      string `json:"sourceId"`
	SourceVersion int64  `json:"sourceVersion"`
	Reason        string `json:"reason"`
}

type SplitRequest struct {
	EvidenceIDs []string `json:"evidenceIds"`
	Title       string   `json:"title"`
	Reason      string   `json:"reason"`
}

type Pagination struct {
	Continue string `json:"continue,omitempty"`
}

type Page[T any] struct {
	Items      []T        `json:"items"`
	Pagination Pagination `json:"pagination"`
}

type Overview struct {
	Counts     map[string]int `json:"counts"`
	Trend      map[string]int `json:"trend"`
	ByPlatform map[string]int `json:"byPlatform"`
	Coverage   string         `json:"coverage"`
}

func validSeverity(value string) bool {
	return value == "P1" || value == "P2" || value == "P3"
}

func validStatus(value string) bool {
	return value == Pending || value == Investigating || value == Recovered || value == Closed
}

func allowedTransition(before, after string) bool {
	if before == after {
		return true
	}
	switch before {
	case Pending:
		return after == Investigating
	case Investigating:
		return after == Recovered
	case Recovered:
		return after == Closed || after == Investigating
	}
	return false
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 253 || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\*?#") && strings.IndexFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) < 0
}

func (s Scope) Validate() error {
	if !validID(s.ClusterID) || !validID(s.Namespace) ||
		(s.BusinessID != "" && !validID(s.BusinessID)) || (s.EnvironmentID != "" && !validID(s.EnvironmentID)) {
		return ErrBadScope
	}
	return nil
}

func displayKey(year int, number int64) string {
	return fmt.Sprintf("INC-%04d-%05d", year, number)
}

func incidentPlatform(i Incident) string {
	switch i.Resource.Kind {
	case "Host":
		return "zabbix"
	case "ECS":
		return "aliyun-ecs"
	case "CVM":
		return "tencent-cvm"
	default:
		return "kubernetes"
	}
}
