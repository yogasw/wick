// Package notes stores markdown notes attached to a ticket or to a
// single session, one JSON file per note.
//
// Notes are what carries knowledge across sessions. When an agent loses
// the thread and you open a fresh session on the same ticket, the notes
// are what the new session can read to pick up where the last one left
// off — written by a person or by the agent itself.
//
// Note bodies are deliberately NOT injected into the system prompt: a
// ticket accumulates notes for as long as the work lasts, and a growing
// prompt would charge that cost on every turn forever. Sessions get a
// one-line pointer ("3 notes, read them with the tickets connector") and
// the agent reads what it needs through MCP.
//
// One file per note, rather than an array inside ticket.json, because
// agents edit notes concurrently: two writers touching different notes
// must not clobber each other.
//
// Layout:
//
//	projects/<projectID>/tickets/<ticketID>/notes/<noteID>.json
//	sessions/<sessionID>/notes/<noteID>.json
package notes

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Audience records who a note was WRITTEN FOR. It is a label, not an
// access rule: the agent reads every audience and sees this field, so it
// knows whether it is looking at a hint for itself or a handover message
// for the next person — and can help improve either.
//
// To keep a note away from the agent entirely, hide it (see Note.Hidden).
const (
	AudienceAI    = "ai"
	AudienceHuman = "human"
	AudienceBoth  = "both"
)

// ValidAudience reports whether a is a known audience label.
func ValidAudience(a string) bool {
	return a == AudienceAI || a == AudienceHuman || a == AudienceBoth
}

// Note is one markdown note.
type Note struct {
	ID   string `json:"id"`
	Body string `json:"body"`
	// Checkable renders the note as a checkbox. This is not a todo with a
	// deadline — it is a note that happens to have a done state, e.g.
	// "confirmed the retry fix on staging".
	Checkable bool   `json:"checkable,omitempty"`
	Done      bool   `json:"done,omitempty"` // only meaningful when Checkable
	Audience  string `json:"audience"`
	// Hidden removes a note from the MCP surface: the agent never receives
	// it. The UI keeps showing it, blurred, behind an eye toggle — hiding
	// is not deleting, and it can be undone.
	Hidden    bool      `json:"hidden,omitempty"`
	Author    string    `json:"author,omitempty"` // user ID, or "agent"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// SourceSessionID records which session wrote this note. Set when the
	// note was written from a session (rather than on the ticket itself),
	// and it is what lets a DETACH take back only that session's notes and
	// leave the rest of the ticket's history in place.
	SourceSessionID string `json:"source_session_id,omitempty"`
	// MovedAt marks a note that travelled here with its session. A reader
	// seeing a note out of its original context should be able to tell.
	MovedAt time.Time `json:"moved_at,omitempty"`
}

// Scope says where a note lives: a ticket (shared by every session on
// that ticket) or one loose session. Exactly one of TicketID/SessionID
// must be set — see Resolve for turning a session into its effective
// scope.
type Scope struct {
	ProjectID string
	TicketID  string
	SessionID string
}

// Dir returns the directory holding this scope's notes, or an error when
// the scope is empty or names both a ticket and a session (two possible
// homes for one file is a bug, not something to guess at).
func (s Scope) Dir(layout config.Layout) (string, error) {
	hasTicket := s.TicketID != ""
	hasSession := s.SessionID != ""
	switch {
	case hasTicket && hasSession:
		return "", fmt.Errorf("notes scope names both a ticket and a session")
	case hasTicket:
		if s.ProjectID == "" {
			return "", fmt.Errorf("a ticket scope needs a project id")
		}
		return layout.TicketNotesDir(s.ProjectID, s.TicketID), nil
	case hasSession:
		return layout.SessionNotesDir(s.SessionID), nil
	default:
		return "", fmt.Errorf("notes scope is empty: set a ticket or a session")
	}
}

// AddOptions describes a new note.
type AddOptions struct {
	Body      string
	Checkable bool
	Audience  string // defaults to both
	Author    string
	// SessionID records which session wrote the note, even when it lands
	// in a ticket's scope. Needed so detaching that session can take its
	// own notes back without stripping the ticket of everyone else's.
	SessionID string
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func notePath(dir, id string) string { return filepath.Join(dir, id+".json") }

// Add writes a new note into the scope.
func Add(layout config.Layout, sc Scope, opt AddOptions) (Note, error) {
	dir, err := sc.Dir(layout)
	if err != nil {
		return Note{}, err
	}
	body := strings.TrimSpace(opt.Body)
	if body == "" {
		return Note{}, fmt.Errorf("note body is required")
	}
	audience := opt.Audience
	if audience == "" {
		// Defaulting to "human" would create a silent failure: someone
		// writes a note expecting the agent to use it, and it never does.
		audience = AudienceBoth
	}
	if !ValidAudience(audience) {
		return Note{}, fmt.Errorf("invalid audience %q (want ai, human, or both)", audience)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Note{}, err
	}
	id, err := newID()
	if err != nil {
		return Note{}, err
	}
	now := time.Now().UTC()
	n := Note{
		ID:              id,
		Body:            body,
		Checkable:       opt.Checkable,
		Audience:        audience,
		Author:          opt.Author,
		SourceSessionID: opt.SessionID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := storage.WriteJSON(notePath(dir, id), &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

// List returns every note in the scope, oldest first — notes read as a
// running log, so chronological order is the useful one. Hidden notes ARE
// included: this is the UI's view. Use ListForAgent for the MCP surface.
func List(layout config.Layout, sc Scope) ([]Note, error) {
	dir, err := sc.Dir(layout)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Note, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var n Note
		if rerr := storage.ReadJSON(filepath.Join(dir, e.Name()), &n); rerr != nil {
			continue // one unreadable note must not break the list
		}
		out = append(out, n)
	}
	// NEWEST FIRST. This is the panel's view, and what someone opening a
	// ticket wants is what just happened — on a long-running ticket the
	// useful note was at the bottom of a scroll. The agent's view below
	// re-sorts, because a running log reads forwards.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			// Ids break the tie so the order is stable rather than however
			// ReadDir happened to return two notes written in the same tick.
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// ListForAgent is the MCP view: every audience, minus hidden notes, OLDEST
// first. The filter lives here rather than in the connector handlers so no
// future op can forget to apply it.
//
// The order is deliberately the opposite of List's. These are read as a
// running record of what has been learned, and a record reads forwards —
// "then we found X, so we tried Y". The panel leads with the newest instead,
// because a person opening a ticket is looking for what just happened.
func ListForAgent(layout config.Layout, sc Scope) ([]Note, error) {
	all, err := List(layout, sc)
	if err != nil {
		return nil, err
	}
	out := all[:0:len(all)]
	for _, n := range all {
		if !n.Hidden {
			out = append(out, n)
		}
	}
	// List hands back newest-first; reverse in place to read forwards.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Get reads one note.
func Get(layout config.Layout, sc Scope, id string) (Note, error) {
	dir, err := sc.Dir(layout)
	if err != nil {
		return Note{}, err
	}
	if id == "" {
		return Note{}, fmt.Errorf("note id is required")
	}
	var n Note
	if rerr := storage.ReadJSON(notePath(dir, id), &n); rerr != nil {
		return Note{}, fmt.Errorf("note %q not found", id)
	}
	return n, nil
}

// UpdateOptions is a partial update: nil fields are left alone, which is
// what lets the UI send only what the user touched.
type UpdateOptions struct {
	Body      *string
	Checkable *bool
	Audience  *string
	Hidden    *bool
}

// Update applies a partial change and bumps UpdatedAt.
func Update(layout config.Layout, sc Scope, id string, opt UpdateOptions) (Note, error) {
	dir, err := sc.Dir(layout)
	if err != nil {
		return Note{}, err
	}
	n, err := Get(layout, sc, id)
	if err != nil {
		return Note{}, err
	}
	if opt.Body != nil {
		body := strings.TrimSpace(*opt.Body)
		if body == "" {
			return Note{}, fmt.Errorf("note body cannot be emptied — delete the note instead")
		}
		n.Body = body
	}
	if opt.Audience != nil {
		if !ValidAudience(*opt.Audience) {
			return Note{}, fmt.Errorf("invalid audience %q (want ai, human, or both)", *opt.Audience)
		}
		n.Audience = *opt.Audience
	}
	if opt.Checkable != nil {
		n.Checkable = *opt.Checkable
		if !n.Checkable {
			n.Done = false // a note that is no longer a task cannot be done
		}
	}
	if opt.Hidden != nil {
		n.Hidden = *opt.Hidden
	}
	n.UpdatedAt = time.Now().UTC()
	if err := storage.WriteJSON(notePath(dir, id), &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

// Check sets the done state of a checkable note. Refused on a plain note:
// silently marking something done that has no done state would leave the
// caller believing it recorded a fact it did not.
func Check(layout config.Layout, sc Scope, id string, done bool) (Note, error) {
	dir, err := sc.Dir(layout)
	if err != nil {
		return Note{}, err
	}
	n, err := Get(layout, sc, id)
	if err != nil {
		return Note{}, err
	}
	if !n.Checkable {
		return Note{}, fmt.Errorf("note %q is not checkable", id)
	}
	n.Done = done
	n.UpdatedAt = time.Now().UTC()
	if err := storage.WriteJSON(notePath(dir, id), &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

// Delete removes a note for good. Hiding is the reversible option; this
// is not.
func Delete(layout config.Layout, sc Scope, id string) error {
	dir, err := sc.Dir(layout)
	if err != nil {
		return err
	}
	if _, gerr := Get(layout, sc, id); gerr != nil {
		return gerr
	}
	return os.Remove(notePath(dir, id))
}

// Count summarises a scope for the session-start pointer.
type Count struct {
	// Visible counts notes the agent can actually read (hidden excluded),
	// so the pointer never advertises something it cannot fetch.
	Visible int
	// OpenTasks counts checkable notes not yet done.
	OpenTasks int
}

// Counts summarises the scope's notes.
func Counts(layout config.Layout, sc Scope) (Count, error) {
	all, err := ListForAgent(layout, sc)
	if err != nil {
		return Count{}, err
	}
	c := Count{Visible: len(all)}
	for _, n := range all {
		if n.Checkable && !n.Done {
			c.OpenTasks++
		}
	}
	return c, nil
}

// write persists a note into a scope under its existing id. Used by the
// move path, which keeps ids stable so a note's history survives the trip.
func write(layout config.Layout, sc Scope, n Note) error {
	dir, err := sc.Dir(layout)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return storage.WriteJSON(notePath(dir, n.ID), &n)
}
