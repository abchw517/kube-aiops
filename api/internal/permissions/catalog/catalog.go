// Package catalog defines the stable operation codes shown in the Portal.
package catalog

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed catalog.json
var document []byte

const (
	CatalogRead  = "system.catalog.read"
	FindingRead  = "diagnosis.finding.read"
	ReviewRead   = "diagnosis.review.read"
	ReviewRun    = "diagnosis.review.run"
	ReviewChat   = "diagnosis.review.chat"
	ReviewSave   = "diagnosis.review.save"
	DocumentRead = "knowledge.document.read"
	VersionRead  = "knowledge.document.version"
	WikiAsk      = "knowledge.qa.ask"
	SyncRun      = "knowledge.sync.run"
)

type Point struct {
	Code       string `json:"code"`
	Action     string `json:"action"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Label      string `json:"label"`
	Risk       string `json:"risk"`
	Restricted bool   `json:"restricted"`
	Scope      string `json:"-"`
}

var points = load()

func load() map[string]Point {
	var data struct {
		Rows []struct {
			Scope  string
			Points []Point
		}
	}
	if err := json.Unmarshal(document, &data); err != nil {
		panic(err)
	}
	result := make(map[string]Point)
	for _, row := range data.Rows {
		for _, p := range row.Points {
			if _, exists := result[p.Code]; exists || p.Code == "" {
				panic("duplicate or missing permission code: " + p.Code)
			}
			p.Scope = row.Scope
			result[p.Code] = p
		}
	}
	return result
}

func Find(code string) (Point, bool) {
	p, ok := points[code]
	return p, ok
}

func Serve(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(document)
}
