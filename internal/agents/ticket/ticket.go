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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Status keys are per PROJECT — a team names its own stages. These
// constants are the built-in set every project starts with, re-exported
// from the project package so callers that only import this one can still
// name them.
const (
	StatusOpen       = project.StatusOpen
	StatusInProgress = project.StatusInProgress
	StatusWaiting    = project.StatusWaiting
	StatusDone       = project.StatusDone
)

// DefaultStatuses is the built-in board order, for callers with no project
// config to hand (a test fixture, a fallback path). Anything acting on a
// real project reads cfg.StatusKeys() instead — a project may have renamed
// or replaced every one of these.
func DefaultStatuses() []string {
	cfg := project.TicketConfig{}
	return cfg.StatusKeys()
}

// ValidStatus reports whether s is one of the project's statuses.
func ValidStatus(cfg project.TicketConfig, s string) bool {
	return cfg.HasStatus(s)
}

// statusMinCount is the floor: a board needs at least one column to put a
// ticket in.
const statusMinCount = 1

// ValidateStatuses checks a status list before it is stored.
//
// Two rules, both structural. A board needs at least one column, and
// exactly one status must be terminal: without it auto-resolve has nowhere
// to move a finished ticket and the followup timer would nag work that is
// already done.
func ValidateStatuses(list []project.TicketStatus) error {
	if len(list) == 0 {
		return nil // empty means "use the built-in set"
	}
	if len(list) < statusMinCount {
		return fmt.Errorf("a board needs at least one status")
	}
	seen := map[string]bool{}
	terminals := 0
	for i, s := range list {
		where := fmt.Sprintf("status %d", i+1)
		key := strings.TrimSpace(s.Key)
		if key == "" {
			return fmt.Errorf("%s: key is required", where)
		}
		if !statusKeyOK(key) {
			return fmt.Errorf("%s: key %q must be lowercase letters, digits, or underscores", where, key)
		}
		if seen[key] {
			return fmt.Errorf("%s: duplicate key %q", where, key)
		}
		seen[key] = true
		if s.Terminal {
			terminals++
		}
	}
	if terminals != 1 {
		return fmt.Errorf(
			"exactly one status must be marked as the finished stage (found %d) — "+
				"auto-resolve and the follow-up timer need to know which one means done",
			terminals,
		)
	}
	return nil
}

// statusKeyOK holds keys to a slug shape. A key is stored on every ticket
// and accepted by the MCP surface, so it has to survive being typed and
// quoted; the human-facing wording lives in Label.
func statusKeyOK(key string) bool {
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
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
	// ID adopts an external identifier instead of generating one. It exists
	// so a ticket mirroring a Notion page can BE that page — no mapping to
	// store, and creating from the same page twice collides instead of
	// silently opening a second ticket. Empty means "generate one", which
	// is what every internal caller wants. See NormalizeID for the shape.
	ID       string
	Title    string
	Status   string // defaults to open
	Assignee string
	Fields   map[string]string
	// Sessions optionally seeds the session list (used when a ticket is
	// created from an existing conversation).
	Sessions []string
	// Actor is who is creating this, for the outbound webhook envelope.
	// Zero value reports as "system", which is right for internal writes
	// (auto-create, the sweeper) and wrong for nothing.
	Actor Actor
}

// idAlphabet excludes I, O, 0, and 1 — a ticket code gets read aloud and
// retyped, and those four are where that goes wrong.
const idAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// customIDRe is the charset a caller-supplied id must fit. A ticket id
// becomes a directory name, so this is the same traversal-safe family
// session ids use, with a length cap because the id also has to render on
// a board card. Leading dots and ".." are refused separately.
var customIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// uuidRe matches a uuid once its dashes are stripped.
var uuidRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// generatedIDRe matches the shape newID mints. A caller may not claim one:
// the generator has to stay free to mint any code without checking whether
// somebody reserved it by hand.
var generatedIDRe = regexp.MustCompile(`^T-[` + idAlphabet + `]{4}$`)

// NormalizeID canonicalises a caller-supplied ticket id.
//
// Any id in the safe charset is kept verbatim — "TIK-2026-001" stays what
// the caller typed, because an id adopted from another system is only
// useful if it survives the trip unchanged.
//
// The one exception is a uuid, which is folded to dashless lowercase.
// Notion hands the same page id out in two shapes (dashless 32-hex in a
// page URL, dashed uuid from its API) and they must not become two
// tickets: collapsing both to one form is what makes "create from this
// page" idempotent no matter which shape the caller copied.
func NormalizeID(id string) (string, error) {
	s := strings.TrimSpace(id)
	if s == "" {
		return "", fmt.Errorf("ticket id is empty")
	}
	if strings.HasPrefix(s, ".") || strings.Contains(s, "..") || !customIDRe.MatchString(s) {
		return "", fmt.Errorf("invalid ticket id %q (allowed: [A-Za-z0-9._-], up to 64 characters)", id)
	}
	if generatedIDRe.MatchString(s) {
		return "", fmt.Errorf("ticket id %q is reserved for generated codes", id)
	}
	if h := strings.ToLower(strings.ReplaceAll(s, "-", "")); uuidRe.MatchString(h) {
		return h, nil
	}
	return s, nil
}

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
	// Statuses belong to the project, so they are read from it rather than
	// assumed: a project may have replaced every built-in one.
	p, perr := project.Load(layout, opt.ProjectID)
	if perr != nil {
		return Ticket{}, fmt.Errorf("load project %q: %w", opt.ProjectID, perr)
	}
	cfg := p.Meta.Ticket
	status := strings.TrimSpace(opt.Status)
	if status == "" {
		status = cfg.FirstStatus()
	}
	if !ValidStatus(cfg, status) {
		return Ticket{}, fmt.Errorf("invalid status %q (want %s)", status, strings.Join(cfg.StatusKeys(), ", "))
	}

	now := time.Now().UTC()
	write := func(id string) (Ticket, error) {
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
		emit(Event{Event: EventCreated, Ticket: tk, Actor: opt.Actor.orSystem()})
		return tk, nil
	}

	// A caller-supplied id is a claim on one specific slot, so a taken slot
	// is an error rather than something to retry past: the point of adopting
	// an external id is that creating twice from the same source is caught.
	if raw := strings.TrimSpace(opt.ID); raw != "" {
		id, err := NormalizeID(raw)
		if err != nil {
			return Ticket{}, err
		}
		if Exists(layout, opt.ProjectID, id) {
			return Ticket{}, fmt.Errorf("ticket %q already exists", id)
		}
		return write(id)
	}

	for attempt := 0; attempt < 8; attempt++ {
		id, err := newID()
		if err != nil {
			return Ticket{}, err
		}
		if storage.PathExists(layout.TicketFile(opt.ProjectID, id)) {
			continue
		}
		return write(id)
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
	return SaveAs(layout, tk, Actor{})
}

// SaveAs is Save with the actor named, so the outbound event can say who
// moved the ticket. Save stays as the unattributed form because most
// internal callers genuinely have nobody to name.
func SaveAs(layout config.Layout, tk Ticket, actor Actor) error {
	tk.UpdatedAt = time.Now().UTC()
	return saveEmitting(layout, tk, actor)
}

// saveEmitting writes tk and fires the events its diff implies.
//
// The pre-image is read before the write so the envelope can carry a real
// before/after. A ticket that cannot be read (first write, corrupt file) is
// simply saved without a diff rather than failing: losing an event is a far
// better outcome than losing the edit.
func saveEmitting(layout config.Layout, tk Ticket, actor Actor) error {
	before, err := Load(layout, tk.ProjectID, tk.ID)
	hadBefore := err == nil
	if werr := SaveKeepingTimestamp(layout, tk); werr != nil {
		return werr
	}
	if !hadBefore {
		return nil
	}
	changes := diff(before, tk)
	for _, name := range EventsFor(changes) {
		emit(Event{Event: name, Ticket: tk, Changes: changes, Actor: actor.orSystem()})
	}
	return nil
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
	emit(Event{Event: EventSessionAttached, Ticket: tk, Session: sessionID, Actor: Actor{}.orSystem()})
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
	if tk, lerr := Load(layout, projectID, ticketID); lerr == nil {
		emit(Event{Event: EventSessionDetached, Ticket: tk, Session: sessionID, Actor: Actor{}.orSystem()})
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
	// Read before removing: the event carries the ticket that was deleted,
	// which is the only copy a receiver will ever get of it.
	tk, lerr := Load(layout, projectID, ticketID)
	if err := os.RemoveAll(layout.TicketDir(projectID, ticketID)); err != nil {
		return err
	}
	if lerr == nil {
		emit(Event{Event: EventDeleted, Ticket: tk, Actor: Actor{}.orSystem()})
	}
	return nil
}

// OrphanedStatuses returns the status keys that would be left holding
// tickets if a project's statuses were replaced by next.
//
// Renaming a stage is cheap; losing sight of the tickets in it is not. So a
// status still in use cannot simply be dropped: the caller is told which
// ones, and moves those tickets first. Silently rewriting them would change
// data nobody looked at, and there is no "unassigned status" to park them
// in — a ticket always sits in a column.
func OrphanedStatuses(layout config.Layout, projectID string, next []project.TicketStatus) []string {
	if len(next) == 0 {
		return nil // back to the built-in set; nothing to strand
	}
	keep := make(map[string]bool, len(next))
	for _, s := range next {
		keep[strings.TrimSpace(s.Key)] = true
	}
	tickets, err := List(layout, projectID)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range tickets {
		if keep[t.Status] || seen[t.Status] {
			continue
		}
		seen[t.Status] = true
		out = append(out, t.Status)
	}
	sort.Strings(out)
	return out
}
