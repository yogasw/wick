package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// todo.go is the durable record of the checklists the `todo` tool writes.
//
// The tool used to only ECHO its checklist back: the list existed as a
// tool_use event in the trace and nowhere else. That is enough to render a
// card mid-conversation and not enough for anything else — reload the page
// and the current list is wherever it happens to sit in the scrollback, a
// second person joining the session cannot see what is being worked on, and
// a list from an hour ago is indistinguishable from the live one.
//
// So the list is written here, next to goal.json, in the same shape the tool
// received it: one ACTIVE list plus the ones that came before it. File:
// <SessionDir>/todos.json.
const todoFileName = "todos.json"

// historyLimit caps how many finished lists are kept. A long session can
// work through many checklists; the oldest stop being useful long before
// they stop taking space.
const todoHistoryLimit = 20

// TodoSubstep is one nested step under an item.
type TodoSubstep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

// TodoItem mirrors the tool's item shape, including the deprecated `step`
// label so a list recorded by an older caller still reads correctly.
type TodoItem struct {
	ID          string        `json:"id,omitempty"`
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Step        string        `json:"step,omitempty"`
	Status      string        `json:"status"`
	Substeps    []TodoSubstep `json:"substeps,omitempty"`
}

// Label is what the item is called, whichever field carried it.
func (t TodoItem) Label() string {
	if t.Title != "" {
		return t.Title
	}
	return t.Step
}

// TodoList is one checklist over its lifetime — the same list as it was
// updated, not one entry per update.
type TodoList struct {
	Items     []TodoItem `json:"items"`
	StartedAt time.Time  `json:"started_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	// Done is true when every item is completed. Kept on the record rather
	// than derived by each reader, so "was this finished or abandoned?"
	// survives into the history.
	Done bool `json:"done"`
}

// Todos is the session's whole checklist record.
type Todos struct {
	Active  *TodoList  `json:"active,omitempty"`
	History []TodoList `json:"history,omitempty"`
}

func todoPath(layout agentconfig.Layout, id string) string {
	return filepath.Join(layout.SessionDir(id), todoFileName)
}

// LoadTodos reads the session's checklists. Missing file → empty record, not
// an error: a session that has never used the tool is the normal case.
func LoadTodos(layout agentconfig.Layout, id string) (*Todos, error) {
	if id == "" {
		return &Todos{}, nil
	}
	b, err := os.ReadFile(todoPath(layout, id))
	if err != nil {
		if os.IsNotExist(err) {
			return &Todos{}, nil
		}
		return nil, err
	}
	var t Todos
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// RecordTodos stores a checklist the tool just received.
//
// The hard part is deciding whether a call UPDATES the active list or STARTS
// a new one — the tool carries no list id, so the items have to answer it. A
// call that mentions any item the active list already has is the same list
// moving forward (that is what a checkpoint looks like: same steps, one more
// ticked). A call that shares nothing with it is a new list, and the old one
// goes to history rather than being overwritten and lost.
func RecordTodos(layout agentconfig.Layout, id string, items []TodoItem) (*Todos, error) {
	if id == "" {
		return nil, fmt.Errorf("todo: empty session id")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("todo: no items")
	}
	rec, err := LoadTodos(layout, id)
	if err != nil {
		// A corrupt file must not stop the session from tracking todos —
		// start a fresh record rather than failing every call from here on.
		rec = &Todos{}
	}
	now := time.Now().UTC()

	next := TodoList{Items: items, StartedAt: now, UpdatedAt: now, Done: allCompleted(items)}
	if rec.Active != nil {
		if sharesItem(rec.Active.Items, items) {
			next.StartedAt = rec.Active.StartedAt // same list, carrying on
		} else {
			rec.History = append(rec.History, *rec.Active)
			if len(rec.History) > todoHistoryLimit {
				rec.History = rec.History[len(rec.History)-todoHistoryLimit:]
			}
		}
	}
	rec.Active = &next

	if err := saveTodos(layout, id, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func saveTodos(layout agentconfig.Layout, id string, rec *Todos) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	dir := layout.SessionDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(todoPath(layout, id), b, 0o644)
}

func allCompleted(items []TodoItem) bool {
	for _, it := range items {
		if it.Status != "completed" {
			return false
		}
	}
	return len(items) > 0
}

// sharesItem reports whether two checklists have an item in common — by id
// when ids are given, otherwise by label. Labels are compared
// case-insensitively and trimmed, because a model re-sending its list often
// re-types it.
func sharesItem(a, b []TodoItem) bool {
	seenID := map[string]bool{}
	seenLabel := map[string]bool{}
	for _, it := range a {
		if it.ID != "" {
			seenID[it.ID] = true
		}
		if l := normalizeLabel(it.Label()); l != "" {
			seenLabel[l] = true
		}
	}
	for _, it := range b {
		if it.ID != "" && seenID[it.ID] {
			return true
		}
		if l := normalizeLabel(it.Label()); l != "" && seenLabel[l] {
			return true
		}
	}
	return false
}

func normalizeLabel(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
