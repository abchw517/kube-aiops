package holmes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Workspace struct{ db *sql.DB }

func OpenWorkspace(db *sql.DB) (*Workspace, error) {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS holmes_workspace (
  kind TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL,
  document TEXT NOT NULL, PRIMARY KEY(kind,id)
 )`)
	return &Workspace{db}, err
}

type runtimeKey struct{}
type skillTraceKey struct{}
type skillRequestKey struct{}

func WithSkillRequest(ctx context.Context, message string) context.Context {
	return context.WithValue(ctx, skillRequestKey{}, strings.ToLower(message))
}

func TraceSkills(ctx context.Context) (context.Context, *[]string) {
	names := []string{}
	return context.WithValue(ctx, skillTraceKey{}, &names), &names
}

func recordSkills(ctx context.Context, notes []skillReference) {
	if names, ok := ctx.Value(skillTraceKey{}).(*[]string); ok {
		for _, note := range notes {
			*names = append(*names, note.Name)
		}
	}
}

type skillReference struct {
	Name           string `json:"name"`
	Guidance       string `json:"guidance"`
	manualID       string
	manualRevision int
}

type workspaceEvidence struct {
	Evidence json.RawMessage  `json:"evidence"`
	Skills   []skillReference `json:"approvedReadOnlyReferences"`
}

type runtime struct {
	workspace     *Workspace
	canReadManual func(context.Context, string, int, Scope) bool
}

func WithWorkspace(ctx context.Context, w *Workspace, canReadManual func(context.Context, string, int, Scope) bool) context.Context {
	return context.WithValue(ctx, runtimeKey{}, runtime{w, canReadManual})
}

func WorkspaceFrom(ctx context.Context) *Workspace {
	state, _ := ctx.Value(runtimeKey{}).(runtime)
	return state.workspace
}

var ErrConflict = errors.New("AI 工作区记录已变更，请刷新后重试")

type Entry[T any] struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Data    T      `json:"data"`
}

func GetEntry[T any](ctx context.Context, w *Workspace, kind, id string) (Entry[T], error) {
	entry := Entry[T]{ID: id}
	var doc string
	err := w.db.QueryRowContext(ctx, `SELECT version,document FROM holmes_workspace WHERE kind=? AND id=?`, kind, id).Scan(&entry.Version, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, nil
	}
	if err != nil {
		return entry, err
	}
	err = json.Unmarshal([]byte(doc), &entry.Data)
	return entry, err
}

func ListEntries[T any](ctx context.Context, w *Workspace, kind string) ([]Entry[T], error) {
	rows, err := w.db.QueryContext(ctx, `SELECT id,version,document FROM holmes_workspace WHERE kind=? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry[T]{}
	for rows.Next() {
		var entry Entry[T]
		var doc string
		if err := rows.Scan(&entry.ID, &entry.Version, &doc); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(doc), &entry.Data); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func SaveEntry[T any](ctx context.Context, w *Workspace, kind string, entry Entry[T]) error {
	doc, err := json.Marshal(entry.Data)
	if err != nil {
		return err
	}
	var result sql.Result
	if entry.Version == 0 {
		result, err = w.db.ExecContext(ctx, `INSERT INTO holmes_workspace VALUES (?,?,1,?) ON CONFLICT DO NOTHING`, kind, entry.ID, doc)
	} else {
		result, err = w.db.ExecContext(ctx, `UPDATE holmes_workspace SET version=version+1,document=? WHERE kind=? AND id=? AND version=?`, doc, kind, entry.ID, entry.Version)
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

type ModelConfig struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Fast     string `json:"fast"`
	Accurate string `json:"accurate"`
	Disabled bool   `json:"disabled"`
}

type Connection struct {
	Scope            Scope       `json:"scope"`
	Desired          ModelConfig `json:"desired"`
	Effective        ModelConfig `json:"effective"`
	EffectiveVersion int         `json:"effectiveVersion"`
	PublishedAt      *time.Time  `json:"publishedAt,omitempty"`
}

type Skill struct {
	Scope          Scope  `json:"scope"`
	Name           string `json:"name"`
	Applies        string `json:"applies"`
	Keyword        string `json:"keyword"`
	Body           string `json:"body"`
	ManualID       string `json:"manualId,omitempty"`
	ManualRevision int    `json:"manualRevision,omitempty"`
	Author         string `json:"author"`
	Reviewer       string `json:"reviewer,omitempty"`
	Status         string `json:"status"`
}

func applyWorkspace(ctx context.Context, scope Scope, model string, record []byte, structured bool) (string, any, error) {
	state, _ := ctx.Value(runtimeKey{}).(runtime)
	if state.workspace == nil {
		return model, json.RawMessage(record), nil
	}
	entry, err := GetEntry[Connection](ctx, state.workspace, "connection", ScopeID(scope))
	if err != nil {
		return "", nil, err
	}
	if entry.Data.EffectiveVersion > 0 {
		live := entry.Data.Effective
		if live.Disabled {
			return "", nil, ErrUnconfigured
		}
		model = live.Fast
		if structured || scope.Source == "zabbix" || scope.Source == "alertmanager" {
			model = live.Accurate
		}
	}
	if scope.Source == "wiki" {
		return model, json.RawMessage(record), nil
	}
	skills, err := ListEntries[Skill](ctx, state.workspace, "skill")
	if err != nil {
		return "", nil, err
	}
	var notes []skillReference
	requested, _ := ctx.Value(skillRequestKey{}).(string)
	for _, entry := range skills {
		s := entry.Data
		if s.Scope != scope || s.Status != "enabled" || (!containsFold(record, s.Keyword) && !strings.Contains(requested, strings.ToLower(s.Name))) {
			continue
		}
		if s.ManualID != "" && (state.canReadManual == nil || !state.canReadManual(ctx, s.ManualID, s.ManualRevision, scope)) {
			continue
		}
		notes = append(notes, skillReference{Name: s.Name, Guidance: s.Body, manualID: s.ManualID, manualRevision: s.ManualRevision})
		if len(notes) == 3 {
			break
		}
	}
	if len(notes) == 0 {
		return model, json.RawMessage(record), nil
	}
	return model, workspaceEvidence{Evidence: record, Skills: notes}, nil
}

func skillsCurrent(ctx context.Context, scope Scope, notes []skillReference) bool {
	state, _ := ctx.Value(runtimeKey{}).(runtime)
	for _, note := range notes {
		if note.manualID != "" && (state.canReadManual == nil || !state.canReadManual(ctx, note.manualID, note.manualRevision, scope)) {
			return false
		}
	}
	return true
}

func containsFold(record []byte, keyword string) bool {
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(string(record)), strings.ToLower(keyword))
}

func ScopeID(scope Scope) string {
	doc, _ := json.Marshal(scope)
	return fmt.Sprintf("%x", sha256.Sum256(doc))
}
