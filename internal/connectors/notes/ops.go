package notes

import (
	"fmt"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	notestore "github.com/yogasw/wick/internal/agents/notes"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/pkg/connector"
)

// resolveScope turns the op's scope inputs into a notes scope.
//
// Precedence: an explicit ticket wins, then an explicit session, then the
// calling session — which resolves through the store to the ticket's notes
// when it belongs to one. That last case is the common one and needs no
// arguments at all, so "note this down" just works and lands somewhere the
// next session will look.
func resolveScope(layout agentconfig.Layout, c *connector.Ctx) (notestore.Scope, error) {
	if tid := strings.TrimSpace(c.Input("ticket_id")); tid != "" {
		projectID, err := resolveProject(layout, c, c.Input("project_id"))
		if err != nil {
			return notestore.Scope{}, err
		}
		return notestore.Scope{ProjectID: projectID, TicketID: tid}, nil
	}
	if sid := strings.TrimSpace(c.Input("session_id")); sid != "" {
		return notestore.Resolve(layout, sid)
	}
	sid := c.SessionID()
	if sid == "" {
		return notestore.Scope{}, fmt.Errorf("pass ticket_id or session_id (there is no calling session to infer a scope from)")
	}
	return notestore.Resolve(layout, sid)
}

// resolveProject returns the project a ticket scope belongs to: explicit
// when given, otherwise the calling session's.
func resolveProject(layout agentconfig.Layout, c *connector.Ctx, explicit string) (string, error) {
	if p := strings.TrimSpace(explicit); p != "" {
		return p, nil
	}
	sid := c.SessionID()
	if sid == "" {
		return "", fmt.Errorf("project_id is required with ticket_id (no calling session to infer it from)")
	}
	sess, err := session.Load(layout, sid)
	if err != nil {
		return "", fmt.Errorf("load calling session: %w", err)
	}
	if sess.Meta.ProjectID == "" {
		return "", fmt.Errorf("this session belongs to no project, so project_id must be given")
	}
	return sess.Meta.ProjectID, nil
}

// noteView is the shape ops return. Audience travels with every note so the
// agent knows whether it is reading a hint for itself or a handover message
// for a person — and can improve either.
type noteView struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	Audience  string `json:"audience"`
	Checkable bool   `json:"checkable,omitempty"`
	Done      bool   `json:"done,omitempty"`
	// Author is a NAME, resolved per call. The store keeps the user id (so a
	// rename shows up on old notes), and this is the display side of that —
	// same as the web UI. A uuid told the model nothing it could use.
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"

func view(c *connector.Ctx, n notestore.Note) noteView {
	return noteView{
		ID: n.ID, Body: n.Body, Audience: n.Audience,
		Checkable: n.Checkable, Done: n.Done, Author: authorName(c, n.Author),
		CreatedAt: n.CreatedAt.UTC().Format(rfc3339),
		UpdatedAt: n.UpdatedAt.UTC().Format(rfc3339),
	}
}

// authorName turns a stored author into something a reader can use.
//
// Two stored values are NOT ids and must not be looked up:
//
//	authorUnknown ("unknown") — no human behind the call (cron, system job)
//	"agent"                   — legacy, written before notes recorded the caller
//
// Both surface as "unknown user": naming an actor we cannot identify is worse
// than admitting we cannot. An id that resolves to nothing does the same,
// rather than leaking the uuid the caller cannot read anyway.
func authorName(c *connector.Ctx, stored string) string {
	switch stored {
	case "":
		return ""
	case authorUnknown, "agent":
		return "unknown user"
	}
	if name := c.UserName(stored); name != "" {
		return name
	}
	return "unknown user"
}

// scopeLabel describes where the notes came from, so a reply makes it
// obvious whether a note landed on the ticket (shared) or on this session
// alone.
func scopeLabel(sc notestore.Scope) map[string]any {
	if sc.TicketID != "" {
		return map[string]any{"kind": "ticket", "ticket_id": sc.TicketID, "project_id": sc.ProjectID}
	}
	return map[string]any{"kind": "session", "session_id": sc.SessionID}
}

func (h *handlers) list(c *connector.Ctx) (any, error) {
	sc, err := resolveScope(h.layout, c)
	if err != nil {
		return nil, err
	}
	// ListForAgent, never List: hidden notes must not reach the agent, and
	// the store enforces that so no op here can leak one.
	all, err := notestore.ListForAgent(h.layout, sc)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	out := make([]noteView, 0, len(all))
	for _, n := range all {
		out = append(out, view(c, n))
	}
	return map[string]any{"scope": scopeLabel(sc), "notes": out, "total": len(out)}, nil
}

func (h *handlers) add(c *connector.Ctx) (any, error) {
	sc, err := resolveScope(h.layout, c)
	if err != nil {
		return nil, err
	}
	n, err := notestore.Add(h.layout, sc, notestore.AddOptions{
		Body:      c.Input("body"),
		Checkable: c.InputBool("checkable"),
		Audience:  strings.TrimSpace(c.Input("audience")),
		// The human the agent is acting for, not the literal "agent".
		//
		// A note written through an agent is still that person's note, and
		// labelling every one of them "agent" made the panel unreadable the
		// moment two people used the same conversation — a note from the web UI
		// showed a name while the identical note via an agent showed a role.
		//
		// Stores the USER ID: the UI resolves it to a current name, so a rename
		// is reflected on old notes instead of freezing whatever the name was
		// when the note was written.
		Author: noteAuthor(c),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"scope": scopeLabel(sc), "note": view(c, n)}, nil
}

func (h *handlers) update(c *connector.Ctx) (any, error) {
	sc, err := resolveScope(h.layout, c)
	if err != nil {
		return nil, err
	}
	noteID := strings.TrimSpace(c.Input("note_id"))
	if noteID == "" {
		return nil, fmt.Errorf("note_id is required")
	}
	var opt notestore.UpdateOptions
	if body := c.Input("body"); strings.TrimSpace(body) != "" {
		opt.Body = &body
	}
	if aud := strings.TrimSpace(c.Input("audience")); aud != "" {
		opt.Audience = &aud
	}
	// Checkable arrives as a string so "not passed" stays distinguishable
	// from "passed as false" — a bool input cannot express that.
	switch strings.TrimSpace(strings.ToLower(c.Input("checkable"))) {
	case "true":
		yes := true
		opt.Checkable = &yes
	case "false":
		no := false
		opt.Checkable = &no
	case "":
		// leave unchanged
	default:
		return nil, fmt.Errorf("checkable must be true or false")
	}
	if opt.Body == nil && opt.Audience == nil && opt.Checkable == nil {
		return nil, fmt.Errorf("nothing to update: pass body, audience, and/or checkable")
	}
	n, err := notestore.Update(h.layout, sc, noteID, opt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"scope": scopeLabel(sc), "note": view(c, n)}, nil
}

func (h *handlers) check(c *connector.Ctx) (any, error) {
	sc, err := resolveScope(h.layout, c)
	if err != nil {
		return nil, err
	}
	noteID := strings.TrimSpace(c.Input("note_id"))
	if noteID == "" {
		return nil, fmt.Errorf("note_id is required")
	}
	n, err := notestore.Check(h.layout, sc, noteID, c.InputBool("done"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"scope": scopeLabel(sc), "note": view(c, n)}, nil
}

func (h *handlers) del(c *connector.Ctx) (any, error) {
	sc, err := resolveScope(h.layout, c)
	if err != nil {
		return nil, err
	}
	noteID := strings.TrimSpace(c.Input("note_id"))
	if noteID == "" {
		return nil, fmt.Errorf("note_id is required")
	}
	if err := notestore.Delete(h.layout, sc, noteID); err != nil {
		return nil, err
	}
	return map[string]any{"scope": scopeLabel(sc), "deleted": noteID}, nil
}

// noteAuthor resolves who a note should be attributed to.
//
// CallerUserID is the wick user the call runs on behalf of — resolved by the
// framework from the session owner, so it names the human even though the agent
// is the one making the call.
//
// Empty means there is genuinely no human attached: a cron run, a system job, a
// session predating ownership tracking. authorUnknown is used there rather than
// "agent", which claimed an identity that does not exist and read as if some
// specific actor wrote it.
func noteAuthor(c *connector.Ctx) string {
	if uid := strings.TrimSpace(c.CallerUserID()); uid != "" {
		return uid
	}
	return authorUnknown
}

// authorUnknown marks a note with no human behind it. Distinct from a user id so
// the UI can render it plainly instead of failing to look it up.
const authorUnknown = "unknown"
