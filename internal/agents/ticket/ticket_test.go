package ticket

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	l := config.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(l, project.CreateOptions{ID: "p1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCreateAndLoad(t *testing.T) {
	l := newLayout(t)
	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "Payment webhook failing"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.ID, "T-") || len(tk.ID) != 6 {
		t.Fatalf("id %q should be T- plus 4 chars", tk.ID)
	}
	if tk.Status != StatusOpen {
		t.Fatalf("new ticket status = %q, want open", tk.Status)
	}
	if tk.UpdatedAt.IsZero() || tk.CreatedAt.IsZero() {
		t.Fatal("timestamps not stamped")
	}

	got, err := Load(l, "p1", tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Payment webhook failing" || got.ProjectID != "p1" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestCreateRejectsUnknownProject(t *testing.T) {
	l := newLayout(t)
	if _, err := Create(l, CreateOptions{ProjectID: "nope", Title: "x"}); err == nil {
		t.Fatal("expected an error for an unknown project")
	}
}

func TestCreateRequiresTitle(t *testing.T) {
	l := newLayout(t)
	if _, err := Create(l, CreateOptions{ProjectID: "p1"}); err == nil {
		t.Fatal("expected an error for an empty title")
	}
}

// A short ID is only 4 random chars, so collisions are plausible enough to
// need a retry — this pins that the retry exists and yields distinct ids.
func TestCreateRetriesOnIDCollision(t *testing.T) {
	l := newLayout(t)
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if seen[tk.ID] {
			t.Fatalf("duplicate id issued: %s", tk.ID)
		}
		seen[tk.ID] = true
	}
}

func TestListSortsNewestFirst(t *testing.T) {
	l := newLayout(t)
	a, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "first"})
	b, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "second"})

	got, err := List(l, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d tickets, want 2", len(got))
	}
	// Both present; order is by CreatedAt descending.
	if got[0].ID != b.ID || got[1].ID != a.ID {
		t.Fatalf("order = [%s %s], want [%s %s]", got[0].ID, got[1].ID, b.ID, a.ID)
	}
}

func TestListOfProjectWithoutTicketsIsEmpty(t *testing.T) {
	l := newLayout(t)
	got, err := List(l, "p1")
	if err != nil {
		t.Fatalf("a project with no tickets should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no tickets, got %d", len(got))
	}
}

func TestUpdateBumpsUpdatedAt(t *testing.T) {
	l := newLayout(t)
	tk, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
	before := tk.UpdatedAt

	tk.Status = StatusInProgress
	tk.Assignee = "user-1"
	if err := Save(l, tk); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(l, "p1", tk.ID)
	if got.Status != StatusInProgress || got.Assignee != "user-1" {
		t.Fatalf("update lost: %+v", got)
	}
	if !got.UpdatedAt.After(before) && !got.UpdatedAt.Equal(before) {
		t.Fatalf("UpdatedAt went backwards: %v -> %v", before, got.UpdatedAt)
	}
}

func TestValidStatus(t *testing.T) {
	// An unconfigured project uses the built-in set.
	builtin := project.TicketConfig{}
	for _, ok := range []string{StatusOpen, StatusInProgress, StatusWaiting, StatusDone} {
		if !ValidStatus(builtin, ok) {
			t.Errorf("ValidStatus(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "closed", "OPEN", "in-progress"} {
		if ValidStatus(builtin, bad) {
			t.Errorf("ValidStatus(%q) = true", bad)
		}
	}

	// A board that renamed its stages accepts its own and refuses the
	// built-in ones — otherwise "custom" would be decoration.
	custom := project.TicketConfig{Statuses: []project.TicketStatus{
		{Key: "triage"}, {Key: "shipped", Terminal: true},
	}}
	if !ValidStatus(custom, "triage") {
		t.Error("a configured status must be valid")
	}
	if ValidStatus(custom, StatusOpen) {
		t.Error("a status this board dropped must not be valid")
	}
}

func TestConfigStatusHelpers(t *testing.T) {
	builtin := project.TicketConfig{}
	if got := builtin.FirstStatus(); got != StatusOpen {
		t.Errorf("FirstStatus = %q, want open", got)
	}
	if got := builtin.TerminalStatus(); got != StatusDone {
		t.Errorf("TerminalStatus = %q, want done", got)
	}
	if got := builtin.StatusLabel(StatusInProgress); got != "In Progress" {
		t.Errorf("StatusLabel = %q, want In Progress", got)
	}

	custom := project.TicketConfig{Statuses: []project.TicketStatus{
		{Key: "triage", Label: "Triage"},
		{Key: "shipped", Label: "Shipped", Terminal: true},
	}}
	if got := custom.FirstStatus(); got != "triage" {
		t.Errorf("FirstStatus = %q, want triage", got)
	}
	if got := custom.TerminalStatus(); got != "shipped" {
		t.Errorf("TerminalStatus = %q, want shipped", got)
	}

	// Nothing marked terminal: the last column is the least surprising
	// reading of a board's final stage.
	unmarked := project.TicketConfig{Statuses: []project.TicketStatus{
		{Key: "a"}, {Key: "b"},
	}}
	if got := unmarked.TerminalStatus(); got != "b" {
		t.Errorf("TerminalStatus = %q, want the last column", got)
	}

	// A key with no label still draws as something.
	bare := project.TicketConfig{Statuses: []project.TicketStatus{{Key: "wip", Terminal: true}}}
	if got := bare.StatusLabel("wip"); got != "wip" {
		t.Errorf("StatusLabel = %q, want the key as a fallback", got)
	}
}

func TestAttachAndDetachSession(t *testing.T) {
	l := newLayout(t)
	tk, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})

	if err := AttachSession(l, "p1", tk.ID, "sess-a"); err != nil {
		t.Fatal(err)
	}
	if err := AttachSession(l, "p1", tk.ID, "sess-b"); err != nil {
		t.Fatal(err)
	}
	// Attaching twice must not duplicate the entry.
	if err := AttachSession(l, "p1", tk.ID, "sess-a"); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(l, "p1", tk.ID)
	if len(got.Sessions) != 2 {
		t.Fatalf("sessions = %v, want 2 distinct", got.Sessions)
	}

	if err := DetachSession(l, "p1", tk.ID, "sess-a"); err != nil {
		t.Fatal(err)
	}
	got, _ = Load(l, "p1", tk.ID)
	if len(got.Sessions) != 1 || got.Sessions[0] != "sess-b" {
		t.Fatalf("after detach sessions = %v, want [sess-b]", got.Sessions)
	}

	// Detaching something absent is a no-op, not an error: the caller may be
	// reconciling a stale back-pointer.
	if err := DetachSession(l, "p1", tk.ID, "ghost"); err != nil {
		t.Fatalf("detaching an absent session should be a no-op: %v", err)
	}
}

func TestFindBySessionScansProjectTickets(t *testing.T) {
	l := newLayout(t)
	a, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "a"})
	b, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "b"})
	if err := AttachSession(l, "p1", b.ID, "sess-x"); err != nil {
		t.Fatal(err)
	}

	got, ok := FindBySession(l, "p1", "sess-x")
	if !ok || got.ID != b.ID {
		t.Fatalf("FindBySession = (%v, %v), want ticket %s", got.ID, ok, b.ID)
	}
	if _, ok := FindBySession(l, "p1", "sess-none"); ok {
		t.Fatal("FindBySession found a session that is attached to nothing")
	}
	_ = a
}

func TestDelete(t *testing.T) {
	l := newLayout(t)
	tk, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
	if err := Delete(l, "p1", tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(l, "p1", tk.ID); err == nil {
		t.Fatal("ticket still loadable after delete")
	}
}

func TestValidateStatuses(t *testing.T) {
	ok := []project.TicketStatus{
		{Key: "triage", Label: "Triage"},
		{Key: "shipped", Label: "Shipped", Terminal: true},
	}
	if err := ValidateStatuses(ok); err != nil {
		t.Fatalf("a renamed board should be valid: %v", err)
	}
	// Empty means "use the built-in set", not "no columns".
	if err := ValidateStatuses(nil); err != nil {
		t.Fatalf("empty must be allowed: %v", err)
	}

	cases := []struct {
		name string
		list []project.TicketStatus
		want string
	}{
		{
			"no terminal stage",
			[]project.TicketStatus{{Key: "a"}, {Key: "b"}},
			"exactly one status",
		},
		{
			"two terminal stages",
			[]project.TicketStatus{{Key: "a", Terminal: true}, {Key: "b", Terminal: true}},
			"exactly one status",
		},
		{
			"empty key",
			[]project.TicketStatus{{Key: "  ", Terminal: true}},
			"key is required",
		},
		{
			"duplicate key",
			[]project.TicketStatus{{Key: "a"}, {Key: "a", Terminal: true}},
			"duplicate",
		},
		{
			// Keys are stored on tickets and typed into MCP calls, so they
			// stay slug-shaped; the wording lives in Label.
			"key with spaces",
			[]project.TicketStatus{{Key: "in review", Terminal: true}},
			"lowercase",
		},
		{
			"key with capitals",
			[]project.TicketStatus{{Key: "InReview", Terminal: true}},
			"lowercase",
		},
	}
	for _, c := range cases {
		err := ValidateStatuses(c.list)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want it to mention %q", c.name, err, c.want)
		}
	}

	// One column is enough for a board.
	if err := ValidateStatuses([]project.TicketStatus{{Key: "only", Terminal: true}}); err != nil {
		t.Fatalf("a single terminal status should be valid: %v", err)
	}
}

// Dropping a status that still holds tickets would lose sight of them, so
// the caller is told which ones rather than having the data rewritten.
func TestOrphanedStatuses(t *testing.T) {
	l := newLayout(t)
	if _, err := Create(l, CreateOptions{ProjectID: "p1", Title: "a", Status: StatusOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(l, CreateOptions{ProjectID: "p1", Title: "b", Status: StatusWaiting}); err != nil {
		t.Fatal(err)
	}

	// Keeping "open" but dropping "waiting" strands the second ticket.
	next := []project.TicketStatus{{Key: StatusOpen}, {Key: "shipped", Terminal: true}}
	got := OrphanedStatuses(l, "p1", next)
	if len(got) != 1 || got[0] != StatusWaiting {
		t.Fatalf("orphans = %v, want [waiting]", got)
	}

	// A list that covers every status in use strands nothing.
	full := []project.TicketStatus{
		{Key: StatusOpen}, {Key: StatusWaiting}, {Key: "shipped", Terminal: true},
	}
	if got := OrphanedStatuses(l, "p1", full); len(got) != 0 {
		t.Fatalf("nothing should be stranded, got %v", got)
	}

	// Returning to the built-in set is always safe.
	if got := OrphanedStatuses(l, "p1", nil); len(got) != 0 {
		t.Fatalf("the built-in set strands nothing, got %v", got)
	}
}

// A project that renamed its stages accepts its own keys and refuses the
// built-in ones — that is what "custom" has to mean.
func TestCreateUsesTheProjectsOwnStatuses(t *testing.T) {
	l := newLayout(t)
	p, err := project.Load(l, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p.Meta.Ticket = project.TicketConfig{
		Enabled: true,
		Statuses: []project.TicketStatus{
			{Key: "triage", Label: "Triage"},
			{Key: "shipped", Label: "Shipped", Terminal: true},
		},
	}
	if err := project.SaveMeta(l, "p1", p.Meta); err != nil {
		t.Fatal(err)
	}

	// No status given: the board's FIRST column, not "open".
	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != "triage" {
		t.Fatalf("status = %q, want triage (the first column)", tk.Status)
	}

	if _, err := Create(l, CreateOptions{ProjectID: "p1", Title: "y", Status: "shipped"}); err != nil {
		t.Fatalf("a configured status must be accepted: %v", err)
	}
	if _, err := Create(l, CreateOptions{ProjectID: "p1", Title: "z", Status: StatusOpen}); err == nil {
		t.Fatal("a status this board does not have must be refused")
	}
}

func TestCreateAdoptsExternalID(t *testing.T) {
	l := newLayout(t)
	// The dashed form the Notion API returns.
	tk, err := Create(l, CreateOptions{
		ProjectID: "p1",
		ID:        "1f2e3d4c-5b6a-7988-9a0b-1c2d3e4f5a6b",
		Title:     "Mirror of a Notion page",
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = "1f2e3d4c5b6a79889a0b1c2d3e4f5a6b"
	if tk.ID != want {
		t.Fatalf("id = %q, want the dashless form %q", tk.ID, want)
	}
	if _, lerr := Load(l, "p1", want); lerr != nil {
		t.Fatalf("ticket not readable under its adopted id: %v", lerr)
	}
}

func TestCreateRejectsAdoptedIDTwice(t *testing.T) {
	l := newLayout(t)
	const dashless = "1f2e3d4c5b6a79889a0b1c2d3e4f5a6b"
	if _, err := Create(l, CreateOptions{ProjectID: "p1", ID: dashless, Title: "first"}); err != nil {
		t.Fatal(err)
	}
	// Same page, copied in the other shape and in upper case. It must not
	// open a second ticket — that is the whole reason the id is adopted.
	_, err := Create(l, CreateOptions{
		ProjectID: "p1",
		ID:        "1F2E3D4C-5B6A-7988-9A0B-1C2D3E4F5A6B",
		Title:     "second",
	})
	if err == nil {
		t.Fatal("re-creating from the same page id should be refused")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want it to name the collision", err)
	}
}

func TestCreateRejectsUnusableIDs(t *testing.T) {
	l := newLayout(t)
	for _, id := range []string{
		"../escape",
		"a/b",
		"T-4F2A",
		"not-a-uuid",
		"1f2e3d4c5b6a79889a0b1c2d3e4f5a6",   // 31 chars
		"1f2e3d4c5b6a79889a0b1c2d3e4f5a6bc", // 33 chars
		"1f2e3d4c5b6a79889a0b1c2d3e4f5a6g",  // g is not hex
	} {
		if _, err := Create(l, CreateOptions{ProjectID: "p1", ID: id, Title: "x"}); err == nil {
			t.Fatalf("id %q should be refused", id)
		}
	}
}

func TestCreateWithoutIDStillGenerates(t *testing.T) {
	l := newLayout(t)
	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "no id given"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.ID, "T-") || len(tk.ID) != 6 {
		t.Fatalf("id %q, want the generated T- form to be untouched", tk.ID)
	}
}
