package tickets

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/pkg/connector"
)

// seedProject creates the project a session/ticket needs to exist under.
func seedProject(t *testing.T, layout agentconfig.Layout, id string) {
	t.Helper()
	if _, err := project.Create(layout, project.CreateOptions{ID: id, Name: id}); err != nil {
		t.Fatalf("create project %s: %v", id, err)
	}
}

// seedSession creates a session in a project, optionally already on a ticket.
func seedSession(t *testing.T, layout agentconfig.Layout, id, projectID, owner, ticketID string) {
	t.Helper()
	sess, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:        id,
		ProjectID: projectID,
		UserID:    owner,
	})
	if err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
	if ticketID != "" {
		sess.Meta.TicketID = ticketID
		if err := session.SaveMeta(layout, id, sess.Meta); err != nil {
			t.Fatalf("save meta %s: %v", id, err)
		}
	}
}

func untrackedCtx(t *testing.T, caller string, in map[string]string) *connector.Ctx {
	t.Helper()
	c := connector.NewCtx(context.Background(), "", nil, in, nil, nil, nil)
	c.SetCallerUserID(caller)
	return c
}

// TestUntracked_ListsOnlySessionsWithNoTicket pins the distinction that matters:
// an UNASSIGNED ticket exists but has nobody on it, while an UNTRACKED session
// has no ticket at all. Answering one with the other reports unowned work as
// unfiled work.
func TestUntracked_ListsOnlySessionsWithNoTicket(t *testing.T) {
	layout := agentconfig.Layout{BaseDir: t.TempDir()}
	h := &handlers{layout: layout}
	const proj = "proj-1"
	seedProject(t, layout, proj)
	seedProject(t, layout, "proj-2")

	// One filed session, one not.
	// Sessions on the TICKET is the list of record (see ticket.Ticket.Sessions);
	// Meta.TicketID is only a denormalised back-pointer. Seed both, the way
	// ticket_attach_session does.
	tk, err := ticket.Create(layout, ticket.CreateOptions{
		ProjectID: proj, Title: "filed work", Sessions: []string{"s-filed"},
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	seedSession(t, layout, "s-filed", proj, "user-ada", tk.ID)
	seedSession(t, layout, "s-loose", proj, "user-ada", "")
	// A session in a DIFFERENT project must not leak in.
	seedSession(t, layout, "s-other", "proj-2", "user-ada", "")

	res, err := h.untracked(untrackedCtx(t, "user-ada", map[string]string{"project_id": proj}))
	if err != nil {
		t.Fatalf("untracked: %v", err)
	}
	m := res.(map[string]any)
	if m["total"].(int) != 1 {
		t.Fatalf("total = %v, want 1 (only the unfiled session)", m["total"])
	}
	// The view type is unexported and anonymous, so assert through the JSON
	// shape rather than naming it.
	blob, _ := json.Marshal(m["sessions"])
	if !strings.Contains(string(blob), "s-loose") {
		t.Fatalf("sessions = %s, want the unfiled session", blob)
	}
	if strings.Contains(string(blob), "s-filed") {
		t.Fatalf("sessions = %s, must not include a filed session", blob)
	}
}

// TestUntracked_StaleBackPointerStillCountsAsUntracked: Meta.TicketID is
// denormalised, so a pointer left behind by a deleted ticket would otherwise
// hide a session that IS untracked — the exact thing being asked for.
func TestUntracked_StaleBackPointerStillCountsAsUntracked(t *testing.T) {
	layout := agentconfig.Layout{BaseDir: t.TempDir()}
	h := &handlers{layout: layout}
	const proj = "proj-1"
	seedProject(t, layout, proj)

	// Points at a ticket that never existed.
	seedSession(t, layout, "s-stale", proj, "user-ada", "T-GONE")

	res, err := h.untracked(untrackedCtx(t, "user-ada", map[string]string{"project_id": proj}))
	if err != nil {
		t.Fatalf("untracked: %v", err)
	}
	if total := res.(map[string]any)["total"].(int); total != 1 {
		t.Fatalf("total = %d, want 1; a stale back-pointer hid an untracked session", total)
	}
}

// TestUntracked_MineScopesToCaller and refuses when there is no caller: silently
// scanning the whole project would answer a wider question than was asked.
func TestUntracked_MineScopesToCaller(t *testing.T) {
	layout := agentconfig.Layout{BaseDir: t.TempDir()}
	h := &handlers{layout: layout}
	const proj = "proj-1"
	seedProject(t, layout, proj)

	seedSession(t, layout, "s-ada", proj, "user-ada", "")
	seedSession(t, layout, "s-bob", proj, "user-bob", "")

	// Whole project by default.
	res, err := h.untracked(untrackedCtx(t, "user-ada", map[string]string{"project_id": proj}))
	if err != nil {
		t.Fatalf("untracked: %v", err)
	}
	if total := res.(map[string]any)["total"].(int); total != 2 {
		t.Fatalf("default total = %d, want 2 (whole project)", total)
	}

	// Scoped to the caller.
	res, err = h.untracked(untrackedCtx(t, "user-ada", map[string]string{
		"project_id": proj, "mine": "true",
	}))
	if err != nil {
		t.Fatalf("untracked mine: %v", err)
	}
	if total := res.(map[string]any)["total"].(int); total != 1 {
		t.Fatalf("mine total = %d, want 1", total)
	}

	// mine=true with nobody signed in must refuse rather than widen.
	_, err = h.untracked(untrackedCtx(t, "", map[string]string{
		"project_id": proj, "mine": "true",
	}))
	if err == nil {
		t.Fatal("mine=true with no caller silently scanned the whole project")
	}
}

// TestMine_RefusesWithoutACaller: ticket_mine has no id to fall back on, so
// listing the project instead would answer a different question.
func TestMine_RefusesWithoutACaller(t *testing.T) {
	layout := agentconfig.Layout{BaseDir: t.TempDir()}
	h := &handlers{layout: layout}

	_, err := h.mine(untrackedCtx(t, "", map[string]string{"project_id": "proj-1"}))
	if err == nil {
		t.Fatal("ticket_mine with no caller did not refuse")
	}
	if !strings.Contains(err.Error(), "assignee=") {
		t.Errorf("error should name the alternative: %v", err)
	}
}
