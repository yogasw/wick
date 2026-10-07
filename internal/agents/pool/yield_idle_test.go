package pool

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A spawn about to be admitted first asks idle warm provider servers to
// yield, naming the session and the provider it will run on.
func TestSendYieldsIdleServersBeforeSpawn(t *testing.T) {
	var mu sync.Mutex
	var calls [][3]string
	prev := yieldIdleServers
	yieldIdleServers = func(sessionID, pType, pName string) {
		mu.Lock()
		calls = append(calls, [3]string{sessionID, pType, pName})
		mu.Unlock()
	}
	t.Cleanup(func() { yieldIdleServers = prev })

	sp := &scriptedSpawner{}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S-yield")
	if err := p.Send(context.Background(), "S-yield", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 || calls[0][0] != "S-yield" || calls[0][1] != "claude" {
		t.Fatalf("yield calls = %v", calls)
	}
}

// A send that only queues starts nothing, so it retires no idle server;
// the yield runs for the entry the queue actually grants.
func TestQueuedSendDoesNotYield(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	prev := yieldIdleServers
	yieldIdleServers = func(sessionID, pType, pName string) {
		mu.Lock()
		calls = append(calls, sessionID)
		mu.Unlock()
	}
	t.Cleanup(func() { yieldIdleServers = prev })

	sp := &scriptedSpawner{Lines: [][]string{
		{}, // A holds the only slot until its idle kill
		{`{"type":"system","subtype":"init","session_id":"x"}`, `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`},
	}}
	p, layout := newPool(t, 1, sp)
	setupSession(t, layout, "A")
	setupSession(t, layout, "B")
	if err := p.Send(context.Background(), "A", "default", "ui", "user", "first"); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "B", "default", "ui", "user", "second"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "A" {
		t.Fatalf("yield calls after a queued send = %v, want [A]", got)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 2 && calls[1] == "B"
	}, 5*time.Second)
}
