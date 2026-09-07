package correlation

import "github.com/abchw517/kube-aiops/internal/finding"

type CorrelationSource string

const (
	SourceKubernetesEvents CorrelationSource = "kubernetes-events"
	SourcePrometheus       CorrelationSource = "prometheus"
	SourceVictoriaLogs     CorrelationSource = "victorialogs"
	SourceAlertmanager     CorrelationSource = "alertmanager"
)

type SourceState string

const (
	SourceAvailable   SourceState = "available"
	SourceUnavailable SourceState = "unavailable"
	SourceDisabled    SourceState = "disabled"
	SourcePartial     SourceState = "partial"
)

type SignalType string

const (
	SignalTypeEvent  SignalType = "event"
	SignalTypeMetric SignalType = "metric"
	SignalTypeLog    SignalType = "log"
	SignalTypeAlert  SignalType = "alert"
)

type SignalUnit string

const (
	UnitCount             SignalUnit = "count"
	UnitBytes             SignalUnit = "bytes"
	UnitSeconds           SignalUnit = "seconds"
	UnitMilliseconds      SignalUnit = "milliseconds"
	UnitPercent           SignalUnit = "percent"
	UnitRatio             SignalUnit = "ratio"
	UnitCores             SignalUnit = "cores"
	UnitBytesPerSecond    SignalUnit = "bytes_per_second"
	UnitRequestsPerSecond SignalUnit = "requests_per_second"
)

type TimeWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type CorrelationScope struct {
	Cluster   string              `json:"cluster"`
	Namespace string              `json:"namespace,omitempty"`
	Resource  finding.ResourceRef `json:"resource"`
}

type EvidenceRef struct {
	Source CorrelationSource `json:"source"`
	ID     string            `json:"id"`
}

type CorrelationBudget struct {
	WindowSeconds          int `json:"windowSeconds"`
	PerSourceTimeoutMillis int `json:"perSourceTimeoutMillis"`
	MaxSignalsPerSource    int `json:"maxSignalsPerSource"`
}

type SourceStatus struct {
	Source CorrelationSource `json:"source"`
	State  SourceState       `json:"state"`
}

type CorrelationSignal struct {
	ID          string               `json:"id"`
	Source      CorrelationSource    `json:"source"`
	Type        SignalType           `json:"type"`
	Severity    string               `json:"severity,omitempty"`
	Resource    *finding.ResourceRef `json:"resource,omitempty"`
	FirstSeen   string               `json:"firstSeen,omitempty"`
	LastSeen    string               `json:"lastSeen,omitempty"`
	Count       int                  `json:"count,omitempty"`
	Name        string               `json:"name,omitempty"`
	Value       *float64             `json:"value,omitempty"`
	Unit        SignalUnit           `json:"unit,omitempty"`
	Fingerprint string               `json:"fingerprint,omitempty"`
	Summary     string               `json:"summary,omitempty"`
	Evidence    []EvidenceRef        `json:"evidence,omitempty"`
}

type CorrelationBundle struct {
	FindingID string              `json:"findingId"`
	Scope     CorrelationScope    `json:"scope"`
	Window    TimeWindow          `json:"window"`
	Budget    CorrelationBudget   `json:"budget"`
	Sources   []SourceStatus      `json:"sources"`
	Signals   []CorrelationSignal `json:"signals"`
}

// Request is an internal, backend-owned correlation request. It intentionally contains no raw
// query language, upstream URL, label selector, log line, Kubernetes object or credential field.
type Request struct {
	FindingID  string
	Scope      CorrelationScope
	AnchorTime string
}
