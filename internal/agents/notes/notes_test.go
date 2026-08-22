package notes

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
)

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	l := config.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return l
}

func ticketScope() Scope  { return Scope{ProjectID: "p1", TicketID: "T-ABCD"} }
func sessionScope() Scope { return Scope{SessionID: "sess-1"} }

func TestAddAndList(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()

	n, err := Add(l, sc, AddOptions{Body: "Webhook retries every 30s", Author: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if n.ID == "" || n.CreatedAt.IsZero() {
		t.Fatalf("note not fully stamped: %+v", n)
	}
	if n.Audience != AudienceBoth {
		t.Fatalf("default audience = %q, want both", n.Audience)
	}

	got, err := List(l, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != "Webhook retries every 30s" {
		t.Fatalf("List = %+v", got)
	}
}

func TestAddRequiresBody(t *testing.T) {
	l := newLayout(t)
	if _, err := Add(l, ticketScope(), AddOptions{Body: "   "}); err == nil {
		t.Fatal("expected an error for an empty body")
	}
}

func TestAddRejectsAmbiguousScope(t *testing.T) {
	l := newLayout(t)
	// Both a ticket and a session named: there would be two possible homes
	// for the file, so this must be refused rather than guessed.
	bad := Scope{ProjectID: "p1", TicketID: "T-ABCD", SessionID: "sess-1"}
	if _, err := Add(l, bad, AddOptions{Body: "x"}); err == nil {
		t.Fatal("expected an error for a scope naming both a ticket and a session")
	}
	if _, err := Add(l, Scope{}, AddOptions{Body: "x"}); err == nil {
		t.Fatal("expected an error for an empty scope")
	}
	if _, err := Add(l, Scope{TicketID: "T-ABCD"}, AddOptions{Body: "x"}); err == nil {
		t.Fatal("a ticket scope without a project id must be refused")
	}
}

func TestListNewestLast(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()
	a, _ := Add(l, sc, AddOptions{Body: "first"})
	b, _ := Add(l, sc, AddOptions{Body: "second"})

	got, _ := List(l, sc)
	if len(got) != 2 {
		t.Fatalf("want 2 notes, got %d", len(got))
	}
	// Reading order is chronological: notes are a running log.
	if got[0].ID != a.ID || got[1].ID != b.ID {
		t.Fatalf("order = [%s %s], want [%s %s]", got[0].ID, got[1].ID, a.ID, b.ID)
	}
}

func TestListEmptyScope(t *testing.T) {
	l := newLayout(t)
	got, err := List(l, ticketScope())
	if err != nil {
		t.Fatalf("a scope with no notes must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want none, got %d", len(got))
	}
}

func TestSessionScopeIsSeparateFromTicketScope(t *testing.T) {
	l := newLayout(t)
	if _, err := Add(l, ticketScope(), AddOptions{Body: "on the ticket"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(l, sessionScope(), AddOptions{Body: "on the session"}); err != nil {
		t.Fatal(err)
	}
	tn, _ := List(l, ticketScope())
	sn, _ := List(l, sessionScope())
	if len(tn) != 1 || tn[0].Body != "on the ticket" {
		t.Fatalf("ticket notes = %+v", tn)
	}
	if len(sn) != 1 || sn[0].Body != "on the session" {
		t.Fatalf("session notes = %+v", sn)
	}
}

func TestUpdateBodyAudienceAndHidden(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()
	n, _ := Add(l, sc, AddOptions{Body: "draft"})

	body := "revised"
	aud := AudienceAI
	hidden := true
	got, err := Update(l, sc, n.ID, UpdateOptions{Body: &body, Audience: &aud, Hidden: &hidden})
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "revised" || got.Audience != AudienceAI || !got.Hidden {
		t.Fatalf("update lost: %+v", got)
	}
	if !got.UpdatedAt.After(n.UpdatedAt) && !got.UpdatedAt.Equal(n.UpdatedAt) {
		t.Fatal("UpdatedAt went backwards")
	}
}

func TestUpdateRejectsUnknownAudience(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()
	n, _ := Add(l, sc, AddOptions{Body: "x"})
	bad := "everyone"
	if _, err := Update(l, sc, n.ID, UpdateOptions{Audience: &bad}); err == nil {
		t.Fatal("expected an error for an unknown audience")
	}
}

func TestCheckOnlyAppliesToCheckableNotes(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()
	plain, _ := Add(l, sc, AddOptions{Body: "just a note"})
	task, _ := Add(l, sc, AddOptions{Body: "verify the retry fix", Checkable: true})

	if _, err := Check(l, sc, plain.ID, true); err == nil {
		t.Fatal("checking a non-checkable note should be refused")
	}
	got, err := Check(l, sc, task.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Done {
		t.Fatal("Check(true) did not mark the note done")
	}
	got, _ = Check(l, sc, task.ID, false)
	if got.Done {
		t.Fatal("Check(false) did not clear done")
	}
}

// The MCP surface must never receive a hidden note. Filtering lives in the
// store, not the handler, so no future op can forget to apply it.
func TestListForAgentExcludesHiddenAndKeepsEveryAudience(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()
	aiNote, _ := Add(l, sc, AddOptions{Body: "for ai", Audience: AudienceAI})
	humanNote, _ := Add(l, sc, AddOptions{Body: "for human", Audience: AudienceHuman})
	bothNote, _ := Add(l, sc, AddOptions{Body: "for both", Audience: AudienceBoth})
	secret, _ := Add(l, sc, AddOptions{Body: "private"})
	yes := true
	if _, err := Update(l, sc, secret.ID, UpdateOptions{Hidden: &yes}); err != nil {
		t.Fatal(err)
	}

	got, err := ListForAgent(l, sc)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range got {
		ids[n.ID] = true
	}
	if ids[secret.ID] {
		t.Fatal("a hidden note reached the agent surface")
	}
	// A human-audience note is still readable: the agent should be able to
	// help improve a handover message, it just knows who it was written for.
	for _, want := range []string{aiNote.ID, humanNote.ID, bothNote.ID} {
		if !ids[want] {
			t.Fatalf("note %s missing from the agent surface", want)
		}
	}
}

func TestDelete(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()
	n, _ := Add(l, sc, AddOptions{Body: "x"})
	if err := Delete(l, sc, n.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := List(l, sc)
	if len(got) != 0 {
		t.Fatalf("note survived delete: %+v", got)
	}
	if err := Delete(l, sc, "ghost"); err == nil {
		t.Fatal("deleting an unknown note should error")
	}
}

func TestCountsForPointer(t *testing.T) {
	l := newLayout(t)
	sc := ticketScope()
	if _, err := Add(l, sc, AddOptions{Body: "a"}); err != nil {
		t.Fatal(err)
	}
	task, _ := Add(l, sc, AddOptions{Body: "b", Checkable: true})
	hidden, _ := Add(l, sc, AddOptions{Body: "c"})
	yes := true
	if _, err := Update(l, sc, hidden.ID, UpdateOptions{Hidden: &yes}); err != nil {
		t.Fatal(err)
	}

	c, err := Counts(l, sc)
	if err != nil {
		t.Fatal(err)
	}
	// Hidden notes are invisible to the agent, so the pointer must not
	// advertise them either.
	if c.Visible != 2 {
		t.Fatalf("Visible = %d, want 2", c.Visible)
	}
	if c.OpenTasks != 1 {
		t.Fatalf("OpenTasks = %d, want 1", c.OpenTasks)
	}
	if _, err := Check(l, sc, task.ID, true); err != nil {
		t.Fatal(err)
	}
	c, _ = Counts(l, sc)
	if c.OpenTasks != 0 {
		t.Fatalf("OpenTasks after check = %d, want 0", c.OpenTasks)
	}
}
