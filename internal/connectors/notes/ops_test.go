// Self-test: exercise every notes_* MCP op through the same
// connector.Ctx surface `wick mcp serve` dispatches into.
package notes

import (
	"context"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	notestore "github.com/yogasw/wick/internal/agents/notes"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/pkg/connector"
)

func newTestHandlers(t *testing.T) (*handlers, agentconfig.Layout) {
	t.Helper()
	l := agentconfig.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(l, project.CreateOptions{ID: "p1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	return &handlers{layout: l}, l
}

func mkSession(t *testing.T, l agentconfig.Layout, id, projectID string) {
	t.Helper()
	if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: id, ProjectID: projectID}); err != nil {
		t.Fatal(err)
	}
}

func ctxFor(sessionID string, input map[string]string) *connector.Ctx {
	c := connector.NewCtx(context.Background(), "self-test", nil, input, nil, nil, nil)
	if sessionID != "" {
		c.SetSessionID(sessionID)
	}
	return c
}

func mustDispatch(t *testing.T, h func(*connector.Ctx) (any, error), c *connector.Ctx) map[string]any {
	t.Helper()
	res, err := h(c)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	out, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", res)
	}
	return out
}

// The default scope is the calling session, and for a session on a ticket
// that resolves to the TICKET — which is what makes a note survive into the
// next session.
func TestAddWithNoScopeLandsOnTheTicket(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}

	res := mustDispatch(t, h.add, ctxFor("s1", map[string]string{"body": "root cause is the retry loop"}))
	scope := res["scope"].(map[string]any)
	if scope["kind"] != "ticket" || scope["ticket_id"] != tk.ID {
		t.Fatalf("note landed on %v, want the ticket", scope)
	}

	// Visible from a different session on the same ticket.
	mkSession(t, l, "s2", "p1")
	if err := ticket.AttachSession(l, "p1", tk.ID, "s2"); err != nil {
		t.Fatal(err)
	}
	got := mustDispatch(t, h.list, ctxFor("s2", map[string]string{}))
	if got["total"].(int) != 1 {
		t.Fatalf("second session sees %v notes, want 1", got["total"])
	}
}

func TestAddOnLooseSessionStaysOnTheSession(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")

	res := mustDispatch(t, h.add, ctxFor("s1", map[string]string{"body": "note to self"}))
	scope := res["scope"].(map[string]any)
	if scope["kind"] != "session" || scope["session_id"] != "s1" {
		t.Fatalf("note landed on %v, want the session", scope)
	}
}

func TestAddDefaultsAudienceToBoth(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	res := mustDispatch(t, h.add, ctxFor("s1", map[string]string{"body": "x"}))
	if got := res["note"].(noteView).Audience; got != notestore.AudienceBoth {
		t.Fatalf("audience = %q, want both", got)
	}
}

func TestAddRejectsUnknownAudience(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	if _, err := h.add(ctxFor("s1", map[string]string{"body": "x", "audience": "everyone"})); err == nil {
		t.Fatal("expected an error for an unknown audience")
	}
}

func TestAddWithoutScopeOrSessionFails(t *testing.T) {
	h, _ := newTestHandlers(t)
	_, err := h.add(ctxFor("", map[string]string{"body": "x"}))
	if err == nil {
		t.Fatal("expected an error with no scope and no calling session")
	}
	if !strings.Contains(err.Error(), "ticket_id or session_id") {
		t.Fatalf("error should name the missing inputs, got: %v", err)
	}
}

// Every audience is readable — the agent can improve a note written for a
// person — but a hidden note must never appear.
func TestListShowsEveryAudienceButNeverHidden(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	sc := notestore.Scope{SessionID: "s1"}
	for _, aud := range []string{notestore.AudienceAI, notestore.AudienceHuman, notestore.AudienceBoth} {
		if _, err := notestore.Add(l, sc, notestore.AddOptions{Body: "note for " + aud, Audience: aud}); err != nil {
			t.Fatal(err)
		}
	}
	secret, err := notestore.Add(l, sc, notestore.AddOptions{Body: "private"})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if _, err := notestore.Update(l, sc, secret.ID, notestore.UpdateOptions{Hidden: &yes}); err != nil {
		t.Fatal(err)
	}

	got := mustDispatch(t, h.list, ctxFor("s1", map[string]string{}))
	if got["total"].(int) != 3 {
		t.Fatalf("total = %v, want 3 (hidden excluded)", got["total"])
	}
	for _, n := range got["notes"].([]noteView) {
		if n.ID == secret.ID {
			t.Fatal("a hidden note reached the agent surface")
		}
	}
}

func TestUpdateBodyAndAudience(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	n, err := notestore.Add(l, notestore.Scope{SessionID: "s1"}, notestore.AddOptions{Body: "draft"})
	if err != nil {
		t.Fatal(err)
	}

	res := mustDispatch(t, h.update, ctxFor("s1", map[string]string{
		"note_id": n.ID, "body": "sharpened", "audience": notestore.AudienceHuman,
	}))
	got := res["note"].(noteView)
	if got.Body != "sharpened" || got.Audience != notestore.AudienceHuman {
		t.Fatalf("update lost: %+v", got)
	}
}

func TestUpdateWithNothingToChangeFails(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	n, _ := notestore.Add(l, notestore.Scope{SessionID: "s1"}, notestore.AddOptions{Body: "x"})
	if _, err := h.update(ctxFor("s1", map[string]string{"note_id": n.ID})); err == nil {
		t.Fatal("expected an error when no field was given")
	}
}

// "checkable" travels as a string so "false" stays distinguishable from
// "not passed" — a bool input cannot express that.
func TestUpdateCanTurnCheckableOff(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	sc := notestore.Scope{SessionID: "s1"}
	n, _ := notestore.Add(l, sc, notestore.AddOptions{Body: "task", Checkable: true})
	if _, err := notestore.Check(l, sc, n.ID, true); err != nil {
		t.Fatal(err)
	}

	res := mustDispatch(t, h.update, ctxFor("s1", map[string]string{"note_id": n.ID, "checkable": "false"}))
	got := res["note"].(noteView)
	if got.Checkable || got.Done {
		t.Fatalf("clearing checkable must also clear done: %+v", got)
	}
}

func TestUpdateRejectsBadCheckableValue(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	n, _ := notestore.Add(l, notestore.Scope{SessionID: "s1"}, notestore.AddOptions{Body: "x"})
	if _, err := h.update(ctxFor("s1", map[string]string{"note_id": n.ID, "checkable": "maybe"})); err == nil {
		t.Fatal("expected an error for a non-boolean checkable")
	}
}

func TestCheckOnlyWorksOnCheckableNotes(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	sc := notestore.Scope{SessionID: "s1"}
	plain, _ := notestore.Add(l, sc, notestore.AddOptions{Body: "just a note"})
	task, _ := notestore.Add(l, sc, notestore.AddOptions{Body: "verify the fix", Checkable: true})

	if _, err := h.check(ctxFor("s1", map[string]string{"note_id": plain.ID, "done": "true"})); err == nil {
		t.Fatal("checking a plain note should be refused")
	}
	res := mustDispatch(t, h.check, ctxFor("s1", map[string]string{"note_id": task.ID, "done": "true"}))
	if !res["note"].(noteView).Done {
		t.Fatal("check did not mark the note done")
	}
}

func TestDelete(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	n, _ := notestore.Add(l, notestore.Scope{SessionID: "s1"}, notestore.AddOptions{Body: "x"})

	res := mustDispatch(t, h.del, ctxFor("s1", map[string]string{"note_id": n.ID}))
	if res["deleted"] != n.ID {
		t.Fatalf("deleted = %v, want %s", res["deleted"], n.ID)
	}
	left := mustDispatch(t, h.list, ctxFor("s1", map[string]string{}))
	if left["total"].(int) != 0 {
		t.Fatalf("note survived delete: %v", left)
	}
}

// An explicit ticket_id works without any calling session, which is how a
// human-driven or cross-session call reaches a ticket's notes.
func TestExplicitTicketScopeWorksWithoutASession(t *testing.T) {
	h, l := newTestHandlers(t)
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	res := mustDispatch(t, h.add, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "body": "from outside",
	}))
	scope := res["scope"].(map[string]any)
	if scope["ticket_id"] != tk.ID {
		t.Fatalf("scope = %v, want ticket %s", scope, tk.ID)
	}
}

func TestExplicitTicketWithoutProjectAndWithoutSessionFails(t *testing.T) {
	h, _ := newTestHandlers(t)
	if _, err := h.add(ctxFor("", map[string]string{"ticket_id": "T-ABCD", "body": "x"})); err == nil {
		t.Fatal("expected an error: a ticket scope needs a project id")
	}
}
