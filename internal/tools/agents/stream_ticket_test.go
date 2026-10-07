package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// countingEmitter records what the wrapped (webhook) emitter received.
type countingEmitter struct{ got []ticket.Event }

func (c *countingEmitter) Emit(ev ticket.Event) { c.got = append(c.got, ev) }

func TestTicketStreamEmitterTeesAndSignalsOnlyChanges(t *testing.T) {
	prev := globalBcast
	globalBcast = NewBroadcaster()
	t.Cleanup(func() { globalBcast = prev })
	ch, unsub := globalBcast.Subscribe("")
	defer unsub()

	next := &countingEmitter{}
	e := WithTicketStreamSignal(next)
	e.Emit(ticket.Event{Event: ticket.EventUpdated, ProjectID: "P1", Ticket: ticket.Ticket{ID: "T-1", Title: "secret title"}})
	e.Emit(ticket.Event{Event: ticket.EventBoardAction, ProjectID: "P1"})
	e.Emit(ticket.Event{Event: ticket.EventAction, ProjectID: "P1", Ticket: ticket.Ticket{ID: "T-1"}})

	if len(next.got) != 3 {
		t.Fatalf("webhook emitter got %d events, want all 3", len(next.got))
	}
	got := drain(ch)
	if len(got) != 1 {
		t.Fatalf("signals = %+v, want exactly one (button clicks change nothing)", got)
	}
	if got[0].Type != evTicketChanged || got[0].Data != `{"project_id":"P1","ticket_id":"T-1"}` {
		t.Fatalf("signal = %+v", got[0])
	}
}

// End to end over GET /stream/sessions: a non-admin gets the ticket
// signal for a project they own and never for another user's project.
func TestSessionsStreamTicketSignalIsAccessChecked(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	for id, owner := range map[string]string{"PMINE": "u1", "PTHEIRS": "u2"} {
		if _, err := project.Create(layout, project.CreateOptions{ID: id, Name: id, OwnerUserID: owner}); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	prevMgr, prevBcast, prevPool := globalMgr, globalBcast, globalPool
	globalMgr, globalBcast = registry.NewManager(reg), NewBroadcaster()
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	t.Cleanup(func() { globalMgr, globalBcast, globalPool = prevMgr, prevBcast, prevPool })

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/stream/sessions", nil).WithContext(ctx)
	r = r.WithContext(login.WithUser(r.Context(), &entity.User{ID: "u1"}, nil))
	w := httptest.NewRecorder()
	c := tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); sessionsLifecycleSSE(c) }()
	time.Sleep(50 * time.Millisecond)

	e := WithTicketStreamSignal(nil)
	e.Emit(ticket.Event{Event: ticket.EventCreated, ProjectID: "PMINE", Ticket: ticket.Ticket{ID: "T-MINE", Title: "secret"}})
	e.Emit(ticket.Event{Event: ticket.EventCreated, ProjectID: "PTHEIRS", Ticket: ticket.Ticket{ID: "T-THEIRS"}})
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()

	body := w.Body.String()
	if !strings.Contains(body, "event: ticket\ndata: {\"project_id\":\"PMINE\",\"ticket_id\":\"T-MINE\"}") {
		t.Fatalf("own project's ticket signal missing:\n%s", body)
	}
	for _, leak := range []string{"PTHEIRS", "T-THEIRS", "secret"} {
		if strings.Contains(body, leak) {
			t.Fatalf("%s leaked onto the stream:\n%s", leak, body)
		}
	}
}

// The `pool` signal is bare and coalesced: a burst of transitions in
// another user's session yields one `{}` and never that session's id.
func TestSessionsStreamPoolSignalIsBareAndCoalesced(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	prevMgr, prevBcast, prevPool, prevEvery := globalMgr, globalBcast, globalPool, poolSignalEvery
	globalMgr, globalBcast = registry.NewManager(reg), NewBroadcaster()
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	poolSignalEvery = 40 * time.Millisecond
	t.Cleanup(func() { globalMgr, globalBcast, globalPool, poolSignalEvery = prevMgr, prevBcast, prevPool, prevEvery })

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/stream/sessions", nil).WithContext(ctx)
	r = r.WithContext(login.WithUser(r.Context(), &entity.User{ID: "u1"}, nil))
	w := httptest.NewRecorder()
	c := tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); sessionsLifecycleSSE(c) }()
	time.Sleep(20 * time.Millisecond)
	for range 5 {
		globalBcast.PublishLifecycle(context.Background(), "SOMEONE-ELSES", "a", "working", "claude", 7)
	}
	time.Sleep(150 * time.Millisecond)
	cancel()
	wg.Wait()

	body := w.Body.String()
	if n := strings.Count(body, "event: pool\ndata: {}\n\n"); n != 1 {
		t.Fatalf("pool signals = %d, want 1 for one burst:\n%s", n, body)
	}
	if strings.Contains(body, "SOMEONE-ELSES") {
		t.Fatalf("pool signal named a session:\n%s", body)
	}
}
