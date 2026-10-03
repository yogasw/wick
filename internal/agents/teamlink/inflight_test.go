package teamlink

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// gatedTurns holds each turn until the gate named by its text opens.
type gatedTurns struct {
	mu    sync.Mutex
	gates map[string]chan struct{}
}

func (g *gatedTurns) gate(text string) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gates[text] == nil {
		g.gates[text] = make(chan struct{})
	}
	return g.gates[text]
}

func (g *gatedTurns) Run(_ context.Context, a Peer, text string) (string, string, error) {
	// text is framed: "Message from X (@x):\n<body>"
	for body := range g.snapshot() {
		if len(text) >= len(body) && text[len(text)-len(body):] == body {
			<-g.gate(body)
		}
	}
	return "sess-" + a.ID, "ok", nil
}

func (g *gatedTurns) snapshot() map[string]chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]chan struct{}, len(g.gates))
	for k, v := range g.gates {
		out[k] = v
	}
	return out
}

func waitInflight(t *testing.T, h *Hub, agentID string, n int) {
	t.Helper()
	for i := 0; i < 400; i++ {
		h.mu.Lock()
		got := len(h.inflight[agentID])
		h.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("inflight[%s] never reached %d", agentID, n)
}

// Two messages answered by Anton at once: each keeps its own chain, the
// end of one does not clear the other, and Anton's onward message is held
// to the deeper chain.
func TestConcurrentInboundKeepsEachChain(t *testing.T) {
	h, _, _ := newTestHub(nil)
	g := &gatedTurns{gates: map[string]chan struct{}{}}
	h.Turns = g
	short, deep := g.gate("from captain"), g.gate("from vera")
	ctx := context.Background()

	// Vera is herself two hops deep, so her message to Anton is depth 3.
	h.mu.Lock()
	h.inflight["a-vera"] = map[a2a.TaskID]inbound{"x": {contextID: "chain-v", depth: 2}}
	h.contexts["chain-v"] = &contextState{turns: 2, touched: h.now(), owner: "u1"}
	h.mu.Unlock()

	if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "from captain", Wait: -1}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-vera", To: "anton", Text: "from vera", Wait: -1}); err != nil {
		t.Fatal(err)
	}
	waitInflight(t, h, "a-anton", 2)

	// The depth-1 task ends; the depth-3 one must still be there.
	close(short)
	waitInflight(t, h, "a-anton", 1)
	_, err := h.Send(ctx, SendInput{CallerAgentID: "a-anton", To: "captain", Text: "onward"})
	if !errors.Is(err, ErrHopLimit) {
		t.Fatalf("onward from the depth-3 chain: err = %v, want hop limit", err)
	}
	close(deep)
	waitInflight(t, h, "a-anton", 0)
}

// With the caller's session known, its own chain is used even when a
// deeper one runs elsewhere.
func TestInboundOfPrefersCallerSession(t *testing.T) {
	h, _, _ := newTestHub(nil)
	h.inflight["a-anton"] = map[a2a.TaskID]inbound{
		"t1": {contextID: "c1", depth: 1, session: "s-main"},
		"t2": {contextID: "c2", depth: 3, session: "s-other"},
	}
	if in, _ := h.inboundOf("a-anton", "s-main"); in.contextID != "c1" {
		t.Fatalf("picked %+v", in)
	}
	if in, _ := h.inboundOf("a-anton", ""); in.contextID != "c2" {
		t.Fatalf("unknown session picked %+v, want the deepest", in)
	}
}
