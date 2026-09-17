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

// TodoProgress is how far one item has got. A checklist answers "which step
// are we on"; this answers "how far into that step" — the difference between
// a spinner and a bar you can read a finishing time off.
type TodoProgress struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Label string `json:"label,omitempty"` // "packages", "MB", …
}

// TodoDetail is whatever the step wants to show that a one-line label
// cannot: a log tail, a JSON result, a small table. Format tells the UI how
// to render it; the body is stored as it arrived and truncated, never
// interpreted here.
type TodoDetail struct {
	Format string `json:"format,omitempty"` // text | markdown | json | html | xml
	Body   string `json:"body"`
}

// maxDetailRunes caps one item's detail. A build log is not a checklist
// entry, and todos.json is read in full on every panel open.
const maxDetailRunes = 8000

// TodoItem mirrors the tool's item shape, including the deprecated `step`
// label so a list recorded by an older caller still reads correctly.
type TodoItem struct {
	ID          string        `json:"id,omitempty"`
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Step        string        `json:"step,omitempty"`
	Status      string        `json:"status"`
	Substeps    []TodoSubstep `json:"substeps,omitempty"`
	Progress    *TodoProgress `json:"progress,omitempty"`
	Detail      *TodoDetail   `json:"detail,omitempty"`
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
	// Stopped marks a list somebody gave up on — the job died, the run was
	// cancelled, the card was stuck. Distinct from Done: "nobody is working
	// on this" and "this finished" look identical in a half-ticked list, and
	// only one of them means the work happened.
	Stopped   bool      `json:"stopped,omitempty"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	Note      string    `json:"note,omitempty"`
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

	for i := range items {
		items[i].Detail = clampDetail(items[i].Detail)
	}
	next := TodoList{Items: items, StartedAt: now, UpdatedAt: now, Done: allCompleted(items)}
	if rec.Active != nil {
		if sharesItem(rec.Active.Items, items) && !rec.Active.Stopped {
			next.StartedAt = rec.Active.StartedAt // same list, carrying on
		} else {
			rec.History = appendTodoHistory(rec.History, *rec.Active)
		}
	}
	rec.Active = &next

	if err := saveTodos(layout, id, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// TodoPatch updates ONE item of the active list without resending the rest.
//
// The tool call that writes a whole checklist is the model's shape: it holds
// the list in its head. A build script does not — it knows "stage 2 of 6 is
// running" and nothing about the other five, and making it restate them
// would mean inventing them. So a patch names one item and touches only the
// fields it carries.
type TodoPatch struct {
	// Select is the item's id, or its label when it has no id. An item that
	// is not there yet is appended: a script reporting its first stage
	// should not have to create the list in a separate call.
	Select      string
	Title       string
	Description string
	Status      string
	Progress    *TodoProgress
	Detail      *TodoDetail
	Substeps    []TodoSubstep
}

// PatchTodoItem applies one patch to the session's active checklist,
// creating the list when there is none. Returns the whole record so the
// caller can report the state it produced.
func PatchTodoItem(layout agentconfig.Layout, id string, p TodoPatch) (*Todos, error) {
	if id == "" {
		return nil, fmt.Errorf("todo: empty session id")
	}
	sel := strings.TrimSpace(p.Select)
	if sel == "" {
		return nil, fmt.Errorf("todo: patch needs an item id or title")
	}
	rec, err := LoadTodos(layout, id)
	if err != nil {
		rec = &Todos{}
	}
	now := time.Now().UTC()
	if rec.Active == nil || rec.Active.Stopped {
		// A stopped list is finished business: carry it into history rather
		// than quietly reopening it under the next patch.
		if rec.Active != nil {
			rec.History = appendTodoHistory(rec.History, *rec.Active)
		}
		rec.Active = &TodoList{StartedAt: now}
	}
	list := rec.Active

	idx := -1
	for i, it := range list.Items {
		if (it.ID != "" && it.ID == sel) || normalizeLabel(it.Label()) == normalizeLabel(sel) {
			idx = i
			break
		}
	}
	if idx < 0 {
		item := TodoItem{ID: sel, Title: sel, Status: "pending"}
		if p.Title != "" {
			item.Title = p.Title
		}
		list.Items = append(list.Items, item)
		idx = len(list.Items) - 1
	}

	it := &list.Items[idx]
	if p.Title != "" {
		it.Title = p.Title
	}
	if p.Description != "" {
		it.Description = p.Description
	}
	if p.Status != "" {
		it.Status = p.Status
	}
	if p.Progress != nil {
		it.Progress = p.Progress
	}
	if p.Detail != nil {
		it.Detail = clampDetail(p.Detail)
	}
	if len(p.Substeps) > 0 {
		it.Substeps = p.Substeps
	}

	list.UpdatedAt = now
	list.Done = allCompleted(list.Items)
	if err := saveTodos(layout, id, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// StopTodos marks the active list as given up on. The list stays visible —
// a stuck job is worth seeing — but it stops claiming to be in progress, and
// the next checklist starts fresh instead of merging into it.
func StopTodos(layout agentconfig.Layout, id, note string) (*Todos, error) {
	if id == "" {
		return nil, fmt.Errorf("todo: empty session id")
	}
	rec, err := LoadTodos(layout, id)
	if err != nil {
		return nil, err
	}
	if rec.Active == nil {
		return rec, nil
	}
	now := time.Now().UTC()
	rec.Active.Stopped = true
	rec.Active.StoppedAt = now
	rec.Active.UpdatedAt = now
	if note != "" {
		rec.Active.Note = note
	}
	// Anything still mid-flight is no longer mid-flight; leaving an
	// in_progress item under a stopped list is the stuck card all over again.
	for i := range rec.Active.Items {
		if rec.Active.Items[i].Status == "in_progress" {
			rec.Active.Items[i].Status = "stopped"
		}
	}
	if err := saveTodos(layout, id, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// ClearTodos drops the finished lists, and with all=true the active one too.
// The escape hatch for a panel full of runs nobody will look at again.
func ClearTodos(layout agentconfig.Layout, id string, all bool) (*Todos, error) {
	if id == "" {
		return nil, fmt.Errorf("todo: empty session id")
	}
	rec, err := LoadTodos(layout, id)
	if err != nil {
		return nil, err
	}
	rec.History = nil
	if all {
		rec.Active = nil
	}
	if err := saveTodos(layout, id, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// clampDetail keeps one item's payload to something a panel can hold. The
// tail is kept rather than the head: the end of a log is the part that says
// what happened.
func clampDetail(d *TodoDetail) *TodoDetail {
	if d == nil {
		return nil
	}
	out := *d
	if r := []rune(out.Body); len(r) > maxDetailRunes {
		out.Body = "[truncated: kept the last " + fmt.Sprint(maxDetailRunes) + " characters]\n" +
			string(r[len(r)-maxDetailRunes:])
	}
	return &out
}

// appendTodoHistory pushes a finished list onto the history, oldest first,
// capped.
func appendTodoHistory(hist []TodoList, l TodoList) []TodoList {
	hist = append(hist, l)
	if len(hist) > todoHistoryLimit {
		hist = hist[len(hist)-todoHistoryLimit:]
	}
	return hist
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
