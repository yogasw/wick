package ticket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
)

// captureEmitter records events instead of sending them, so the emission
// tests do not need a live HTTP server.
type captureEmitter struct {
	mu     sync.Mutex
	events []Event
}

func (c *captureEmitter) Emit(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *captureEmitter) names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.events))
	for i, e := range c.events {
		out[i] = e.Event
	}
	return out
}

// withCapture installs a capturing emitter for one test and restores the
// previous one, so tests stay independent of each other's wiring.
func withCapture(t *testing.T) *captureEmitter {
	t.Helper()
	prev := emitter
	cap := &captureEmitter{}
	SetEmitter(cap)
	t.Cleanup(func() { SetEmitter(prev) })
	return cap
}

func TestCreateEmitsCreated(t *testing.T) {
	cap := withCapture(t)
	l := newLayout(t)

	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "Payment webhook failing"})
	if err != nil {
		t.Fatal(err)
	}

	got := cap.names()
	if len(got) != 1 || got[0] != EventCreated {
		t.Fatalf("events = %v, want [%s]", got, EventCreated)
	}
	ev := cap.events[0]
	if ev.Ticket.ID != tk.ID {
		t.Errorf("event carries ticket %q, want %q", ev.Ticket.ID, tk.ID)
	}
	if ev.ProjectID != "p1" {
		t.Errorf("project_id = %q, want p1", ev.ProjectID)
	}
	if ev.ID == "" {
		t.Error("delivery id not stamped — a receiver cannot dedupe")
	}
	if ev.DeliveredAt.IsZero() {
		t.Error("delivered_at not stamped")
	}
	if ev.Actor.Type != ActorSystem {
		t.Errorf("actor = %q, want system for an unattributed write", ev.Actor.Type)
	}
}

// A status move must emit BOTH the specific event and the generic one:
// receivers mirroring every edit subscribe to `updated` and must not have to
// enumerate each specific event to stay complete.
func TestStatusChangeEmitsBothEvents(t *testing.T) {
	cap := withCapture(t)
	l := newLayout(t)

	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	tk.Status = StatusInProgress
	if err := SaveAs(l, tk, Actor{Type: ActorUser, ID: "u1"}); err != nil {
		t.Fatal(err)
	}

	got := cap.names()
	want := map[string]bool{EventCreated: true, EventUpdated: true, EventStatusChanged: true}
	if len(got) != 3 {
		t.Fatalf("events = %v, want 3 (created, updated, status_changed)", got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected event %q", n)
		}
	}

	// The diff has to name the transition, not merely say "something moved".
	var status Event
	for _, e := range cap.events {
		if e.Event == EventStatusChanged {
			status = e
		}
	}
	ch, ok := status.Changes["status"]
	if !ok {
		t.Fatal("status_changed carries no status change")
	}
	if ch.From != StatusOpen || ch.To != StatusInProgress {
		t.Errorf("change = %s→%s, want open→in_progress", ch.From, ch.To)
	}
	if status.Actor.ID != "u1" || status.Actor.Type != ActorUser {
		t.Errorf("actor = %+v, want user u1", status.Actor)
	}
}

func TestAssignEmitsAssigned(t *testing.T) {
	cap := withCapture(t)
	l := newLayout(t)

	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	tk.Assignee = "usr_a91f"
	if err := Save(l, tk); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, e := range cap.events {
		if e.Event == EventAssigned {
			found = true
			if e.Changes["assignee"].To != "usr_a91f" {
				t.Errorf("assignee change = %+v", e.Changes["assignee"])
			}
		}
	}
	if !found {
		t.Fatalf("no %s event in %v", EventAssigned, cap.names())
	}
}

// A save that changes nothing must stay silent, or every poll-driven write
// would spam receivers with no-op events.
func TestSaveWithoutChangesEmitsNothing(t *testing.T) {
	cap := withCapture(t)
	l := newLayout(t)

	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(l, tk); err != nil {
		t.Fatal(err)
	}

	if got := cap.names(); len(got) != 1 || got[0] != EventCreated {
		t.Fatalf("events = %v, want only [%s]", got, EventCreated)
	}
}

func TestDeleteEmitsDeletedWithTheTicket(t *testing.T) {
	cap := withCapture(t)
	l := newLayout(t)

	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "Gone soon"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Delete(l, "p1", tk.ID); err != nil {
		t.Fatal(err)
	}

	last := cap.events[len(cap.events)-1]
	if last.Event != EventDeleted {
		t.Fatalf("last event = %q, want %s", last.Event, EventDeleted)
	}
	// The deleted ticket is the only copy a receiver will ever get.
	if last.Ticket.Title != "Gone soon" {
		t.Errorf("deleted event lost the ticket body: %+v", last.Ticket)
	}
}

func TestSignAndVerify(t *testing.T) {
	body := []byte(`{"event":"ticket.created"}`)
	sig := Sign("s3cret", body)

	if !Verify("s3cret", body, sig) {
		t.Fatal("signature does not verify against its own body")
	}
	if Verify("wrong", body, sig) {
		t.Error("signature verified under the wrong secret")
	}
	if Verify("s3cret", []byte(`{"event":"tampered"}`), sig) {
		t.Error("signature verified against a tampered body")
	}
	// Re-serialised JSON must NOT verify — the docs warn about exactly this,
	// so the property has to actually hold.
	if Verify("s3cret", []byte(`{ "event": "ticket.created" }`), sig) {
		t.Error("signature verified against a re-serialised body")
	}
}

// WantsEvent is the filter every delivery passes through: an empty list has
// to mean "everything", or a fresh webhook would silently receive nothing.
func TestWebhookEventFilter(t *testing.T) {
	all := project.TicketWebhook{Enabled: true}
	if !all.WantsEvent(EventCreated) || !all.WantsEvent(EventDeleted) {
		t.Error("empty filter should accept every event")
	}

	narrow := project.TicketWebhook{Enabled: true, Events: []string{EventStatusChanged}}
	if !narrow.WantsEvent(EventStatusChanged) {
		t.Error("filter rejected the event it names")
	}
	if narrow.WantsEvent(EventCreated) {
		t.Error("filter accepted an event it does not name")
	}

	off := project.TicketWebhook{Enabled: false}
	if off.WantsEvent(EventCreated) {
		t.Error("disabled webhook still wants events")
	}
}

func TestActiveWebhooksSkipsBlankURL(t *testing.T) {
	cfg := project.TicketConfig{Integrations: project.TicketIntegrations{
		Webhooks: []project.TicketWebhook{
			{ID: "a", URL: "https://abc.com/hook", Enabled: true},
			{ID: "b", URL: "", Enabled: true}, // half-written row in the editor
			{ID: "c", URL: "https://abc.net/hook", Enabled: false},
		},
	}}

	got := cfg.ActiveWebhooks(EventCreated)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("active = %+v, want only the enabled row with a URL", got)
	}
}

// End-to-end: a real HTTP receiver, checking headers and signature.
func TestDeliverSignsAndSetsHeaders(t *testing.T) {
	type got struct {
		event, delivery, sig string
		body                 []byte
	}
	recv := make(chan got, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		recv <- got{
			event:    r.Header.Get(HeaderEvent),
			delivery: r.Header.Get(HeaderDelivery),
			sig:      r.Header.Get(HeaderSignature),
			body:     b,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// AllowPrivate: httptest binds to loopback, which the SSRF guard refuses
	// by default. That guard is exercised separately below.
	d := NewDispatcher(nil)
	d.AllowPrivate = true

	rec := d.deliver(
		project.TicketWebhook{ID: "wh_1", URL: srv.URL, Secret: "s3cret", Enabled: true},
		Event{ID: "evt_1", Event: EventCreated, ProjectID: "p1", DeliveredAt: time.Now().UTC()},
	)

	if !rec.OK || rec.Status != http.StatusOK {
		t.Fatalf("delivery = %+v, want ok 200", rec)
	}
	if rec.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 for a 200", rec.Attempts)
	}

	select {
	case g := <-recv:
		if g.event != EventCreated {
			t.Errorf("%s = %q, want %q", HeaderEvent, g.event, EventCreated)
		}
		if g.delivery != "evt_1" {
			t.Errorf("%s = %q, want evt_1", HeaderDelivery, g.delivery)
		}
		if !Verify("s3cret", g.body, g.sig) {
			t.Errorf("signature %q does not verify over the received body", g.sig)
		}
		var ev Event
		if err := json.Unmarshal(g.body, &ev); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if ev.Event != EventCreated {
			t.Errorf("decoded event = %q", ev.Event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("receiver never got the delivery")
	}
}

// An unsigned webhook must not send the header at all — a receiver checking
// for its presence should be able to tell.
func TestDeliverUnsignedOmitsSignature(t *testing.T) {
	sawSig := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSig <- r.Header.Get(HeaderSignature)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(nil)
	d.AllowPrivate = true
	d.deliver(project.TicketWebhook{ID: "wh_1", URL: srv.URL, Enabled: true},
		Event{ID: "e", Event: EventCreated})

	if sig := <-sawSig; sig != "" {
		t.Errorf("unsigned webhook sent %s = %q", HeaderSignature, sig)
	}
}

// A 4xx is the receiver rejecting the request itself, so retrying just
// repeats the rejection. Only 408/429 and 5xx are worth another attempt.
func TestDeliverDoesNotRetryClientError(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	d := NewDispatcher(nil)
	d.AllowPrivate = true
	rec := d.deliver(project.TicketWebhook{ID: "wh_1", URL: srv.URL, Enabled: true},
		Event{ID: "e", Event: EventCreated})

	if rec.OK {
		t.Error("400 recorded as success")
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("receiver hit %d times, want 1 (no retry on 400)", hits)
	}
}

// The SSRF guard is the reason a user-supplied URL is safe to fetch at all.
func TestCheckURLRefusesPrivateAndBadSchemes(t *testing.T) {
	d := NewDispatcher(nil)

	for _, raw := range []string{
		"http://127.0.0.1:9000/hook",
		"http://localhost/hook",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/hook",
	} {
		if err := d.checkURL(raw); err == nil {
			t.Errorf("checkURL(%q) allowed a private address", raw)
		}
	}

	for _, raw := range []string{
		"file:///etc/passwd",
		"gopher://abc.com/",
		"ftp://abc.com/x",
		"not a url at all",
		"https://",
	} {
		if err := d.checkURL(raw); err == nil {
			t.Errorf("checkURL(%q) allowed a non-http(s) or malformed URL", raw)
		}
	}

	// The guard is liftable for an all-private deployment.
	d.AllowPrivate = true
	if err := d.checkURL("http://127.0.0.1:9000/hook"); err != nil {
		t.Errorf("AllowPrivate should permit loopback, got %v", err)
	}
}

func TestDeliveryLogIsNewestFirstAndBounded(t *testing.T) {
	d := NewDispatcher(nil)
	for i := 0; i < maxLogPerWebhook+5; i++ {
		d.record(Delivery{WebhookID: "wh_1", Event: EventCreated, Attempts: i})
	}

	got := d.Deliveries("wh_1")
	if len(got) != maxLogPerWebhook {
		t.Fatalf("log holds %d, want capped at %d", len(got), maxLogPerWebhook)
	}
	// Newest first: the last recorded attempt has the highest counter.
	if got[0].Attempts != maxLogPerWebhook+4 {
		t.Errorf("first entry attempts = %d, want the newest (%d)", got[0].Attempts, maxLogPerWebhook+4)
	}
}

// Emit resolves the project's webhook list through the injected lookup; a
// project with none configured must not attempt any delivery.
func TestEmitSkipsProjectWithoutWebhooks(t *testing.T) {
	d := NewDispatcher(func(string) (project.TicketConfig, bool) {
		return project.TicketConfig{}, true
	})
	d.Emit(Event{Event: EventCreated, ProjectID: "p1"})

	if got := d.Deliveries("wh_1"); len(got) != 0 {
		t.Errorf("recorded %d deliveries for a project with no webhooks", len(got))
	}
}

// The sweeper's two automations get their own events: a receiver wants to
// tell "the team finished this" from "wick gave up waiting and closed it".
func TestSweeperEmitsFollowupAndAutoResolved(t *testing.T) {
	cap := withCapture(t)
	l := newLayout(t)
	now := time.Now()

	p, err := project.Load(l, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p.Meta.Ticket = cfg(3600, 7*24*3600)
	if err := project.SaveMeta(l, "p1", p.Meta); err != nil {
		t.Fatal(err)
	}

	mk := func(title, status string, ago time.Duration, sessions []string) Ticket {
		item, cerr := Create(l, CreateOptions{
			ProjectID: "p1", Title: title, Status: status, Sessions: sessions,
		})
		if cerr != nil {
			t.Fatal(cerr)
		}
		item.UpdatedAt = now.Add(-ago)
		if serr := SaveKeepingTimestamp(l, item); serr != nil {
			t.Fatal(serr)
		}
		return item
	}
	stale := mk("stale", StatusOpen, 2*time.Hour, []string{"sess-stale"})
	dead := mk("dead", StatusWaiting, 8*24*time.Hour, []string{"sess-dead"})

	sweepOnce(context.Background(), Deps{
		Layout:       l,
		ListProjects: func() ([]project.Project, error) { return []project.Project{p}, nil },
		SendFollowup: func(string, string) error { return nil },
	}, now)

	var followup, resolved *Event
	for i := range cap.events {
		switch cap.events[i].Event {
		case EventFollowup:
			followup = &cap.events[i]
		case EventAutoResolved:
			resolved = &cap.events[i]
		}
	}

	if followup == nil {
		t.Fatalf("no %s event in %v", EventFollowup, cap.names())
	}
	if followup.Ticket.ID != stale.ID {
		t.Errorf("followup names ticket %q, want %q", followup.Ticket.ID, stale.ID)
	}
	if followup.Actor.Type != ActorSystem {
		t.Errorf("followup actor = %q, want system", followup.Actor.Type)
	}

	if resolved == nil {
		t.Fatalf("no %s event in %v", EventAutoResolved, cap.names())
	}
	if resolved.Ticket.ID != dead.ID {
		t.Errorf("auto_resolved names ticket %q, want %q", resolved.Ticket.ID, dead.ID)
	}
	// The transition has to name where it came from, or a receiver cannot
	// tell which column the work was abandoned in.
	ch, ok := resolved.Changes["status"]
	if !ok {
		t.Fatal("auto_resolved carries no status change")
	}
	if ch.From != StatusWaiting || ch.To != StatusDone {
		t.Errorf("change = %s→%s, want waiting→done", ch.From, ch.To)
	}
}

func TestValidEvent(t *testing.T) {
	for _, name := range AllEvents() {
		if !ValidEvent(name) {
			t.Errorf("ValidEvent(%q) = false for a catalogued event", name)
		}
	}
	if ValidEvent("ticket.exploded") {
		t.Error("ValidEvent accepted an unknown event")
	}
}

// Deliver is the custom-button path: synchronous, one endpoint, and the
// event stamps its own id/time when the caller left them empty so the
// receiver can still dedupe on X-Wick-Delivery.
func TestDeliverStampsEnvelopeAndPostsAction(t *testing.T) {
	type got struct {
		event, delivery string
		body            []byte
	}
	recv := make(chan got, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		recv <- got{event: r.Header.Get(HeaderEvent), delivery: r.Header.Get(HeaderDelivery), body: b}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(nil)
	d.AllowPrivate = true

	rec := d.Deliver(
		project.TicketWebhook{ID: "btn:btn_1", URL: srv.URL, Enabled: true},
		Event{Event: EventAction, Action: "btn_1", Ticket: Ticket{ID: "T-4F2A", ProjectID: "p1", Title: "x"}},
	)
	if !rec.OK || rec.Status != http.StatusOK {
		t.Fatalf("delivery = %+v, want ok 200", rec)
	}

	g := <-recv
	if g.event != EventAction {
		t.Errorf("%s = %q, want %q", HeaderEvent, g.event, EventAction)
	}
	if g.delivery == "" {
		t.Error("delivery id was not stamped")
	}
	var ev Event
	if err := json.Unmarshal(g.body, &ev); err != nil {
		t.Fatalf("body is not an event envelope: %v", err)
	}
	if ev.Action != "btn_1" || ev.Ticket.ID != "T-4F2A" || ev.DeliveredAt.IsZero() {
		t.Fatalf("envelope incomplete: %+v", ev)
	}
}
