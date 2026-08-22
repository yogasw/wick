// Self-test: exercise every ticket_* MCP op through the same
// connector.Ctx surface `wick mcp serve` dispatches into.
package tickets

import (
	"context"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
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

// ctxFor builds a Ctx with a calling session, which is what lets ops infer
// the project and ticket without arguments.
func ctxFor(sessionID string, input map[string]string) *connector.Ctx {
	c := connector.NewCtx(context.Background(), "self-test", nil, input, nil, nil, nil)
	if sessionID != "" {
		c.SetSessionID(sessionID)
	}
	return c
}

func mustDispatch(t *testing.T, h func(*connector.Ctx) (any, error), c *connector.Ctx) any {
	t.Helper()
	res, err := h(c)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	return res
}

func TestCreateInfersProjectFromCallingSession(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")

	res := mustDispatch(t, h.create, ctxFor("s1", map[string]string{
		"title":                  "Payment webhook failing",
		"attach_current_session": "true",
	}))
	got, ok := res.(ticketView)
	if !ok {
		t.Fatalf("unexpected result type %T", res)
	}
	if got.ProjectID != "p1" {
		t.Fatalf("project = %q, want p1 (inferred from the session)", got.ProjectID)
	}
	if len(got.Sessions) != 1 || got.Sessions[0] != "s1" {
		t.Fatalf("sessions = %v, want [s1]", got.Sessions)
	}
	// The back-pointer is written so lookups do not have to scan.
	sess, err := session.Load(l, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.TicketID != got.ID {
		t.Fatalf("session back-pointer = %q, want %q", sess.Meta.TicketID, got.ID)
	}
}

func TestCreateWithoutSessionOrProjectFails(t *testing.T) {
	h, _ := newTestHandlers(t)
	if _, err := h.create(ctxFor("", map[string]string{"title": "x"})); err == nil {
		t.Fatal("expected an error when neither project_id nor a calling session is available")
	}
}

func TestGetResolvesTheCallingSessionsTicket(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}

	// No ticket_id: the op must find it from the session.
	got := mustDispatch(t, h.get, ctxFor("s1", map[string]string{})).(ticketView)
	if got.ID != tk.ID {
		t.Fatalf("get resolved %q, want %q", got.ID, tk.ID)
	}
}

func TestGetOnUnattachedSessionSaysSo(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	_, err := h.get(ctxFor("s1", map[string]string{}))
	if err == nil {
		t.Fatal("expected an error for a session attached to no ticket")
	}
	if !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("error should explain the session is unattached, got: %v", err)
	}
}

func TestUpdateRejectsInvalidStatus(t *testing.T) {
	h, l := newTestHandlers(t)
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	_, err := h.update(ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "status": "closed",
	}))
	if err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("want an invalid-status error, got: %v", err)
	}
}

func TestUpdateMergesFieldsAndClearsEmptyOnes(t *testing.T) {
	h, l := newTestHandlers(t)
	tk, _ := ticket.Create(l, ticket.CreateOptions{
		ProjectID: "p1", Title: "work",
		Fields: map[string]string{"priority": "low", "type": "bug"},
	})

	got := mustDispatch(t, h.update, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID,
		"status": ticket.StatusInProgress,
		"fields": `{"priority":"high","type":""}`,
	})).(ticketView)

	if got.Status != ticket.StatusInProgress {
		t.Fatalf("status = %q", got.Status)
	}
	if got.Fields["priority"] != "high" {
		t.Fatalf("priority = %q, want high", got.Fields["priority"])
	}
	if _, still := got.Fields["type"]; still {
		t.Fatalf("an empty value should clear the field, fields = %v", got.Fields)
	}
}

func TestUpdateRejectsMalformedFields(t *testing.T) {
	h, l := newTestHandlers(t)
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	_, err := h.update(ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "fields": "not json",
	}))
	if err == nil {
		t.Fatal("expected an error for malformed fields JSON")
	}
}

func TestListFiltersByStatus(t *testing.T) {
	h, l := newTestHandlers(t)
	if _, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "a", Status: ticket.StatusOpen}); err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "b", Status: ticket.StatusDone}); err != nil {
		t.Fatal(err)
	}

	res := mustDispatch(t, h.list, ctxFor("", map[string]string{
		"project_id": "p1", "status": ticket.StatusDone,
	})).(map[string]any)
	if res["total"].(int) != 1 {
		t.Fatalf("total = %v, want 1", res["total"])
	}
	if res["tickets"].([]ticketView)[0].Title != "b" {
		t.Fatalf("wrong ticket returned: %+v", res["tickets"])
	}
}

func TestListRejectsInvalidStatusFilter(t *testing.T) {
	h, _ := newTestHandlers(t)
	if _, err := h.list(ctxFor("", map[string]string{"project_id": "p1", "status": "nope"})); err == nil {
		t.Fatal("expected an error for an invalid status filter")
	}
}

// Attaching a second session is the recovery move the whole design exists
// for: same ticket, fresh conversation.
func TestAttachAndDetachSecondSession(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	mkSession(t, l, "s2", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work", Sessions: []string{"s1"}})

	got := mustDispatch(t, h.attach, ctxFor("s2", map[string]string{"ticket_id": tk.ID})).(ticketView)
	if len(got.Sessions) != 2 {
		t.Fatalf("sessions = %v, want two", got.Sessions)
	}
	sess, _ := session.Load(l, "s2")
	if sess.Meta.TicketID != tk.ID {
		t.Fatalf("back-pointer not written on attach: %q", sess.Meta.TicketID)
	}

	got = mustDispatch(t, h.detach, ctxFor("s2", map[string]string{"ticket_id": tk.ID})).(ticketView)
	if len(got.Sessions) != 1 || got.Sessions[0] != "s1" {
		t.Fatalf("after detach sessions = %v, want [s1]", got.Sessions)
	}
	sess, _ = session.Load(l, "s2")
	if sess.Meta.TicketID != "" {
		t.Fatalf("back-pointer not cleared on detach: %q", sess.Meta.TicketID)
	}
}

func TestAttachRequiresTicketID(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	if _, err := h.attach(ctxFor("s1", map[string]string{})); err == nil {
		t.Fatal("expected an error without ticket_id")
	}
}

func TestSettingsGetAndSet(t *testing.T) {
	h, l := newTestHandlers(t)

	// Off by default.
	got := mustDispatch(t, h.settingsGet, ctxFor("", map[string]string{"project_id": "p1"})).(settingsView)
	if got.Enabled {
		t.Fatal("ticket mode should start off")
	}

	// Minutes and days in, seconds on disk.
	got = mustDispatch(t, h.settingsSet, ctxFor("", map[string]string{
		"project_id":              "p1",
		"enabled":                 "true",
		"followup_after_minutes":  "90",
		"auto_resolve_after_days": "14",
	})).(settingsView)
	if !got.Enabled || got.FollowupAfterMinutes != 90 || got.AutoResolveAfterDays != 14 {
		t.Fatalf("settings not applied: %+v", got)
	}
	p, err := project.Load(l, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Meta.Ticket.FollowupAfterSec != 5400 || p.Meta.Ticket.AutoResolveAfterSec != 14*86400 {
		t.Fatalf("stored seconds wrong: %+v", p.Meta.Ticket)
	}
	// Enabling with no schema seeds the default fields, so the board is
	// usable straight away.
	if len(got.Fields) == 0 {
		t.Fatal("enabling with no fields should seed the defaults")
	}
}

func TestSettingsSetAutoCreateRules(t *testing.T) {
	h, l := newTestHandlers(t)
	// The case the design exists for: everything from Slack except DMs.
	rules := `[{"origin":"slack","channel_kind":"dm","enabled":false},{"origin":"slack","enabled":true}]`
	got := mustDispatch(t, h.settingsSet, ctxFor("", map[string]string{
		"project_id": "p1", "enabled": "true", "auto_create": rules,
	})).(settingsView)
	if len(got.AutoCreate) != 2 {
		t.Fatalf("rules = %+v, want 2", got.AutoCreate)
	}
	p, _ := project.Load(l, "p1")
	if len(p.Meta.Ticket.AutoCreate) != 2 {
		t.Fatalf("rules not persisted: %+v", p.Meta.Ticket.AutoCreate)
	}

	// [] clears them.
	got = mustDispatch(t, h.settingsSet, ctxFor("", map[string]string{
		"project_id": "p1", "auto_create": "[]",
	})).(settingsView)
	if len(got.AutoCreate) != 0 {
		t.Fatalf("rules should be cleared: %+v", got.AutoCreate)
	}
}

// A rule that could never fire is refused rather than stored looking active.
func TestSettingsSetRejectsBrokenRule(t *testing.T) {
	h, _ := newTestHandlers(t)
	_, err := h.settingsSet(ctxFor("", map[string]string{
		"project_id": "p1", "auto_create": `[{"origin":"*","match":"regex:[unclosed","enabled":true}]`,
	}))
	if err == nil || !strings.Contains(err.Error(), "regex") {
		t.Fatalf("want a regex compile error, got: %v", err)
	}
	_, err = h.settingsSet(ctxFor("", map[string]string{
		"project_id": "p1", "auto_create": `[{"origin":"","enabled":true}]`,
	}))
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("want an origin error, got: %v", err)
	}
}

func TestSettingsSetRejectsMalformedJSON(t *testing.T) {
	h, _ := newTestHandlers(t)
	if _, err := h.settingsSet(ctxFor("", map[string]string{
		"project_id": "p1", "auto_create": "not json",
	})); err == nil {
		t.Fatal("expected an error for malformed auto_create JSON")
	}
}

func TestSettingsSetWithNothingToChangeFails(t *testing.T) {
	h, _ := newTestHandlers(t)
	if _, err := h.settingsSet(ctxFor("", map[string]string{"project_id": "p1"})); err == nil {
		t.Fatal("expected an error when no field was given")
	}
}

// Whoever asks for a ticket is presumed to be taking it on: landing an
// unassigned card in front of the person who just created it says the
// opposite of what they meant.
func TestCreateAssignsToTheCallerByDefault(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")

	c := ctxFor("s1", map[string]string{"title": "Payments down"})
	c.SetCallerUserID("user-7")
	res, err := h.create(c)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(ticketView).Assignee; got != "user-7" {
		t.Fatalf("assignee = %q, want the caller", got)
	}
}

// An explicit value still wins — including an explicitly blank one, which
// is how "nobody yet" is expressed.
func TestCreateHonoursAnExplicitAssignee(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")

	c := ctxFor("s1", map[string]string{"title": "x", "assignee": "someone-else"})
	c.SetRawInput(map[string]any{"assignee": "someone-else"})
	c.SetCallerUserID("user-7")
	res, err := h.create(c)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(ticketView).Assignee; got != "someone-else" {
		t.Fatalf("assignee = %q, want the explicit value", got)
	}

	c2 := ctxFor("s1", map[string]string{"title": "y", "assignee": ""})
	c2.SetRawInput(map[string]any{"assignee": ""})
	c2.SetCallerUserID("user-7")
	res2, err := h.create(c2)
	if err != nil {
		t.Fatal(err)
	}
	if got := res2.(ticketView).Assignee; got != "" {
		t.Fatalf("assignee = %q, want nobody", got)
	}
}
