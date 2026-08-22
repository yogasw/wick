// Package ticket stores tickets as first-class entities under a project
// (`projects/<id>/tickets/<ticketID>/ticket.json`), each holding zero or
// more sessions.
//
// A ticket owns the work; a session is one conversation about it. That is
// why the two are separate: when an agent goes off the rails you start a
// fresh session on the SAME ticket, keeping its status, assignee, fields,
// and notes. An earlier design put the ticket on session.Meta, which made
// that impossible.
//
// The ticket's Sessions slice is the list of record. session.Meta.TicketID
// is a denormalised back-pointer so the sidebar and the pool can answer
// "which ticket is this session in?" without scanning every ticket; when
// the two disagree, this package's copy wins.
package ticket

import (
	"crypto/rand"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Statuses. Fixed set: the board renders one column per status, in this
// order.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusWaiting    = "waiting"
	StatusDone       = "done"
)

// Statuses lists every valid status in board column order.
var Statuses = []string{StatusOpen, StatusInProgress, StatusWaiting, StatusDone}

// ValidStatus reports whether s is one of the fixed statuses.
func ValidStatus(s string) bool {
	for _, v := range Statuses {
		if s == v {
			return true
		}
	}
	return false
}

// Ticket is the persisted shape of one ticket.
type Ticket struct {
	// ID is a short, human-quotable code ("T-4F2A") rather than a UUID:
	// it appears on every board card and gets typed into chat.
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Assignee  string `json:"assignee,omitempty"` // wick user ID
	// Fields holds values keyed by project.TicketField.Key.
	Fields map[string]string `json:"fields,omitempty"`
	// Sessions is the list of record for which sessions belong here.
	Sessions  []string  `json:"sessions,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt tracks the last TICKET edit — not chat activity. The
	// stale-followup and auto-resolve timers run from it.
	UpdatedAt time.Time `json:"updated_at"`
	// LastFollowupAt guards the sweeper against re-sending a followup on
	// every tick; the next one waits another full window.
	LastFollowupAt time.Time `json:"last_followup_at,omitempty"`
}

// CreateOptions describes a new ticket.
type CreateOptions struct {
	ProjectID string
	Title     string
	Status    string // defaults to open
	Assignee  string
	Fields    map[string]string
	// Sessions optionally seeds the session list (used when a ticket is
	// created from an existing conversation).
	Sessions []string
}

// idAlphabet excludes I, O, 0, and 1 — a ticket code gets read aloud and
// retyped, and those four are where that goes wrong.
const idAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newID returns a short code like "T-4F2A".
func newID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := []byte("T-")
	for _, c := range b {
		out = append(out, idAlphabet[int(c)%len(idAlphabet)])
	}
	return string(out), nil
}

// Create materialises a new ticket on disk, retrying on the (plausible,
// with only four random characters) chance of an ID collision.
func Create(layout config.Layout, opt CreateOptions) (Ticket, error) {
	if opt.ProjectID == "" {
		return Ticket{}, fmt.Errorf("project id is required")
	}
	if !project.Exists(layout, opt.ProjectID) {
		return Ticket{}, fmt.Errorf("project %q not found", opt.ProjectID)
	}
	title := strings.TrimSpace(opt.Title)
	if title == "" {
		return Ticket{}, fmt.Errorf("ticket title is required")
	}
	status := opt.Status
	if status == "" {
		status = StatusOpen
	}
	if !ValidStatus(status) {
		return Ticket{}, fmt.Errorf("invalid status %q", status)
	}

	now := time.Now().UTC()
	for attempt := 0; attempt < 8; attempt++ {
		id, err := newID()
		if err != nil {
			return Ticket{}, err
		}
		if storage.PathExists(layout.TicketFile(opt.ProjectID, id)) {
			continue
		}
		tk := Ticket{
			ID:        id,
			ProjectID: opt.ProjectID,
			Title:     title,
			Status:    status,
			Assignee:  strings.TrimSpace(opt.Assignee),
			Fields:    opt.Fields,
			Sessions:  opt.Sessions,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := os.MkdirAll(layout.TicketDir(opt.ProjectID, id), 0o755); err != nil {
			return Ticket{}, err
		}
		if err := storage.WriteJSON(layout.TicketFile(opt.ProjectID, id), &tk); err != nil {
			return Ticket{}, err
		}
		return tk, nil
	}
	return Ticket{}, fmt.Errorf("could not allocate a free ticket id after 8 attempts")
}

// Load reads one ticket.
func Load(layout config.Layout, projectID, ticketID string) (Ticket, error) {
	if projectID == "" || ticketID == "" {
		return Ticket{}, fmt.Errorf("project id and ticket id are required")
	}
	var tk Ticket
	if err := storage.ReadJSON(layout.TicketFile(projectID, ticketID), &tk); err != nil {
		return Ticket{}, err
	}
	return tk, nil
}

// Save rewrites a ticket and bumps UpdatedAt, which is what the stale and
// auto-resolve timers key on. Callers that must NOT move those timers
// (the sweeper stamping LastFollowupAt) use SaveKeepingTimestamp.
func Save(layout config.Layout, tk Ticket) error {
	tk.UpdatedAt = time.Now().UTC()
	return SaveKeepingTimestamp(layout, tk)
}

// SaveKeepingTimestamp persists a ticket exactly as given, leaving
// UpdatedAt alone.
func SaveKeepingTimestamp(layout config.Layout, tk Ticket) error {
	if tk.ProjectID == "" || tk.ID == "" {
		return fmt.Errorf("ticket is missing project id or id")
	}
	if !storage.PathExists(layout.TicketDir(tk.ProjectID, tk.ID)) {
		return fmt.Errorf("ticket %q not found", tk.ID)
	}
	return storage.WriteJSON(layout.TicketFile(tk.ProjectID, tk.ID), &tk)
}

// List returns a project's tickets, newest first. A project with no
// tickets directory yet is empty, not an error.
func List(layout config.Layout, projectID string) ([]Ticket, error) {
	ids, err := storage.ScanDirNames(layout.ProjectTicketsDir(projectID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Ticket, 0, len(ids))
	for _, id := range ids {
		tk, lerr := Load(layout, projectID, id)
		if lerr != nil {
			continue // unreadable ticket must not break the board
		}
		out = append(out, tk)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// Exists reports whether a ticket is on disk.
func Exists(layout config.Layout, projectID, ticketID string) bool {
	if projectID == "" || ticketID == "" {
		return false
	}
	return storage.PathExists(layout.TicketFile(projectID, ticketID))
}

// AttachSession adds a session to a ticket and brings its notes along.
// Idempotent: attaching one that is already there changes nothing, so a
// retry after a partial failure is safe. Does not touch UpdatedAt — adding
// a session is not an edit to the ticket's own state and should not reset
// its staleness.
//
// When the session was on another ticket it is detached from that one
// first, so a session is never on two tickets at once — that is what makes
// dragging a session between cards a move rather than a copy.
func AttachSession(layout config.Layout, projectID, ticketID, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	tk, err := Load(layout, projectID, ticketID)
	if err != nil {
		return err
	}
	for _, s := range tk.Sessions {
		if s == sessionID {
			return nil
		}
	}

	// A session belongs to one ticket. Find the previous owner (if any)
	// before writing, so its notes can be carried across in one step.
	from := ""
	if prev, ok := FindBySession(layout, projectID, sessionID); ok {
		from = prev.ID
		if derr := detachOnly(layout, projectID, prev.ID, sessionID); derr != nil {
			return derr
		}
	}

	tk.Sessions = append(tk.Sessions, sessionID)
	if err := SaveKeepingTimestamp(layout, tk); err != nil {
		return err
	}
	return moveNotes(layout, sessionID, from, ticketID)
}

// DetachSession removes a session from a ticket and moves its notes back
// to the session itself. Removing one that is not there is a no-op, not an
// error: callers reconcile stale back-pointers through this path.
func DetachSession(layout config.Layout, projectID, ticketID, sessionID string) error {
	changed, err := detachAndReport(layout, projectID, ticketID, sessionID)
	if err != nil || !changed {
		return err
	}
	// Notes go back to the session, not into the void: the ticket keeps
	// what other sessions wrote, this session keeps what it wrote.
	return moveNotes(layout, sessionID, ticketID, "")
}

// detachOnly removes a session from a ticket without touching notes. Used
// by AttachSession, which moves the notes itself in one hop rather than
// bouncing them through the session on the way.
func detachOnly(layout config.Layout, projectID, ticketID, sessionID string) error {
	_, err := detachAndReport(layout, projectID, ticketID, sessionID)
	return err
}

// detachAndReport does the list edit and says whether anything changed.
func detachAndReport(layout config.Layout, projectID, ticketID, sessionID string) (bool, error) {
	tk, err := Load(layout, projectID, ticketID)
	if err != nil {
		return false, err
	}
	out := tk.Sessions[:0:len(tk.Sessions)]
	for _, s := range tk.Sessions {
		if s != sessionID {
			out = append(out, s)
		}
	}
	if len(out) == len(tk.Sessions) {
		return false, nil
	}
	tk.Sessions = out
	return true, SaveKeepingTimestamp(layout, tk)
}

// FindBySession returns the ticket a session belongs to by scanning the
// project's tickets. This is the authoritative answer; the back-pointer on
// session meta is only a shortcut, so anything that must be correct (an
// MCP op resolving "this session's ticket") comes through here.
func FindBySession(layout config.Layout, projectID, sessionID string) (Ticket, bool) {
	if sessionID == "" {
		return Ticket{}, false
	}
	tickets, err := List(layout, projectID)
	if err != nil {
		return Ticket{}, false
	}
	for _, tk := range tickets {
		for _, s := range tk.Sessions {
			if s == sessionID {
				return tk, true
			}
		}
	}
	return Ticket{}, false
}

// Delete removes a ticket and everything under it, notes included.
func Delete(layout config.Layout, projectID, ticketID string) error {
	if projectID == "" || ticketID == "" {
		return fmt.Errorf("project id and ticket id are required")
	}
	if !Exists(layout, projectID, ticketID) {
		return fmt.Errorf("ticket %q not found", ticketID)
	}
	return os.RemoveAll(layout.TicketDir(projectID, ticketID))
}
