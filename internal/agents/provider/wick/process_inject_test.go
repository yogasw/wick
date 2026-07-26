package wick

import (
	"context"
	"sync"
	"testing"
	"time"
)

// process_inject_test.go covers the completion inject seam at the process
// level: a job finishing pushes a synthetic turn into msgs, and doing so
// after teardown must never panic on the closed channel.

func newInjectTestProcess() *wickProcess {
	ctx, cancel := context.WithCancel(context.Background())
	return &wickProcess{
		msgs:   make(chan string, 16),
		ctx:    ctx,
		cancel: cancel,
	}
}

// TestInjectTurn_DeliversToMsgs proves an injected turn lands on the msgs
// channel — the mechanism the engine loop reads to run a new turn.
func TestInjectTurn_DeliversToMsgs(t *testing.T) {
	p := newInjectTestProcess()
	p.injectTurn("[job-done id=job_1 ok=true]")
	select {
	case got := <-p.msgs:
		if got != "[job-done id=job_1 ok=true]" {
			t.Errorf("unexpected injected text: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("injected turn never reached msgs")
	}
}

// TestInjectTurn_AfterKillNoPanic is the teardown-race guard: once the
// process is killed (ctx cancelled + msgs closed), a late job-completion
// inject must be a no-op, not a send-on-closed-channel panic.
func TestInjectTurn_AfterKillNoPanic(t *testing.T) {
	p := newInjectTestProcess()
	// Simulate Kill: cancel ctx, then close msgs (same order as Kill).
	p.cancel()
	p.closeMsgs()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Must not panic.
		p.injectTurn("[job-done id=job_late ok=true]")
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("injectTurn blocked after kill")
	}
}

// TestInjectTurn_ConcurrentWithClose hammers the exact race: many injects
// firing while the channel is being closed. With the -race detector this
// catches an unsynchronised send-on-closed. It must complete without
// panic regardless of interleaving.
func TestInjectTurn_ConcurrentWithClose(t *testing.T) {
	p := newInjectTestProcess()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				p.injectTurn("x")
			}
		}()
	}
	// Drain so senders don't all block on a full buffer before close.
	go func() {
		for range p.msgs {
		}
	}()
	time.Sleep(5 * time.Millisecond)
	p.cancel()
	p.closeMsgs()
	wg.Wait()
}
