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
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

/* /stream/multi exists because of arithmetic: a browser allows ~6 concurrent
   HTTP/1.1 connections per origin and an SSE stream holds its slot for the
   life of the page, so one-connection-per-session capped the app at about
   four open conversations before every later request sat at "pending".
   These tests pin the contract that lets the SharedWorker replace N streams
   with one: fan-in across sessions, session_id on every event for routing,
   and per-session ACL that skips rather than fails. */

// withMultiStreamWorld wires the package globals the handler reads
// (registry manager + broadcaster) against a temp layout holding the given
// sessions, and restores everything afterwards.
func withMultiStreamWorld(t *testing.T, sessions map[string]string) {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	for id, userID := range sessions {
		if _, err := session.Create(context.Background(), layout,
			session.CreateOptions{ID: id, Origin: session.OriginUI, UserID: userID}); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}

	prevMgr, prevBcast := globalMgr, globalBcast
	globalMgr = registry.NewManager(reg)
	globalBcast = NewBroadcaster()
	t.Cleanup(func() { globalMgr, globalBcast = prevMgr, prevBcast })
}

// runMultiStream runs the handler for the given user until cancel, then
// hands back everything it wrote.
func runMultiStream(t *testing.T, user *entity.User, target string, publish func()) (int, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
	if user != nil {
		r = r.WithContext(login.WithUser(r.Context(), user, nil))
	}
	w := httptest.NewRecorder()
	c := tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		streamMultiSSE(c)
	}()

	// Let the handler reach its subscribe + first flush before publishing.
	time.Sleep(50 * time.Millisecond)
	if publish != nil {
		publish()
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()
	return w.Code, w.Body.String()
}

func TestStreamMultiFansInAllSubscribedSessions(t *testing.T) {
	withMultiStreamWorld(t, map[string]string{"S1": "", "S2": ""})

	_, body := runMultiStream(t,
		&entity.User{Role: entity.RoleAdmin, IsOwner: true},
		"/stream/multi?sessions=S1,S2",
		func() {
			globalBcast.Publish("S1", "agent", event.AgentEvent{Type: event.TextDelta, Text: "hello-from-S1"})
			globalBcast.Publish("S2", "agent", event.AgentEvent{Type: event.TextDelta, Text: "hello-from-S2"})
		})

	if !strings.Contains(body, "hello-from-S1") || !strings.Contains(body, "hello-from-S2") {
		t.Fatalf("one stream must carry BOTH sessions' events; body:\n%s", body)
	}
	// The worker routes by session_id — without it a multiplexed event is
	// undeliverable, so it is part of the contract, not a nicety.
	if !strings.Contains(body, `"session_id":"S1"`) || !strings.Contains(body, `"session_id":"S2"`) {
		t.Fatalf("every event must carry its session_id for client-side routing; body:\n%s", body)
	}
}

func TestStreamMultiSkipsSessionsTheCallerCannotOpen(t *testing.T) {
	withMultiStreamWorld(t, map[string]string{"MINE": "u1", "THEIRS": "u2"})

	_, body := runMultiStream(t,
		&entity.User{ID: "u1"}, // non-admin: owns MINE, must never see THEIRS
		"/stream/multi?sessions=MINE,THEIRS,GHOST",
		func() {
			globalBcast.Publish("MINE", "agent", event.AgentEvent{Type: event.TextDelta, Text: "visible-event"})
			globalBcast.Publish("THEIRS", "agent", event.AgentEvent{Type: event.TextDelta, Text: "leaked-event"})
		})

	if !strings.Contains(body, "visible-event") {
		t.Fatalf("caller's own session must stream; body:\n%s", body)
	}
	// The whole point of per-session ACL on a multiplexed stream: one
	// request must not become a window into someone else's conversation.
	if strings.Contains(body, "leaked-event") {
		t.Fatalf("another user's session leaked through the multiplexed stream; body:\n%s", body)
	}
}

func TestStreamMultiRejectsWhenNothingIsAccessible(t *testing.T) {
	withMultiStreamWorld(t, map[string]string{"THEIRS": "u2"})

	r := httptest.NewRequest(http.MethodGet, "/stream/multi?sessions=THEIRS,GHOST", nil)
	r = r.WithContext(login.WithUser(r.Context(), &entity.User{ID: "u1"}, nil))
	w := httptest.NewRecorder()
	c := tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)

	streamMultiSSE(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("all-inaccessible request = %d, want 404 (and no held-open stream)", w.Code)
	}
}
