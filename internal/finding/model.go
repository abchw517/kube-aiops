package finding

import "errors"

const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
	DefaultLimit     = 50
	MaxLimit         = 200
)

var (
	ErrInvalidCursor = errors.New("invalid continue token")
	ErrInvalidLimit  = errors.New("invalid finding limit")
	ErrTooMany       = errors.New("too many findings")
)

type ResourceRef struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
}

type Finding struct {
	ID        string      `json:"id"`
	Cluster   string      `json:"cluster"`
	Namespace string      `json:"namespace,omitempty"`
	Severity  string      `json:"severity"`
	Resource  ResourceRef `json:"resource"`
	Problem   string      `json:"problem"`
	Details   string      `json:"details,omitempty"`
	Source    string      `json:"source"`
	CreatedAt string      `json:"createdAt,omitempty"`
}

type Filter struct {
	Cluster   string
	Namespace string
	Kind      string
	Severity  string
	Problem   string
}

type Query struct {
	Filter
	Limit    int
	Continue string
}

type Pagination struct {
	Limit    int    `json:"limit"`
	Continue string `json:"continue,omitempty"`
}

type Page struct {
	Items      []Finding  `json:"items"`
	Pagination Pagination `json:"pagination"`
}

type Summary struct {
	Total       int            `json:"total"`
	BySeverity  map[string]int `json:"bySeverity"`
	ByKind      map[string]int `json:"byKind"`
	ByNamespace map[string]int `json:"byNamespace"`
}

func NormalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultLimit, nil
	}
	if limit < 1 || limit > MaxLimit {
		return 0, ErrInvalidLimit
	}
	return limit, nil
}
