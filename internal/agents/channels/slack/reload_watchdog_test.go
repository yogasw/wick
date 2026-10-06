package slack

import (
	"context"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// A socket run that never reports "connecting" is restarted: the instance
// must not sit silently offline until its settings happen to be saved.
func TestWatchStartRestartsAStuckRun(t *testing.T) {
	old := socketStartTimeout
	socketStartTimeout = 10 * time.Millisecond
	t.Cleanup(func() { socketStartTimeout = old })

	s := New(agentconfig.SlackChannelConfig{})
	runCtx, gen, done := s.beginRun(context.Background())
	defer done()
	s.setSocketState("starting")
	s.watchStart(runCtx, gen)

	// The restart stops the stuck run (state flips to disconnected).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if state, _ := s.SocketState(); state == "disconnected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stuck run was not restarted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runCtx.Err() == nil {
		t.Fatal("stuck run still alive after restart")
	}
}

// A run that got as far as connecting, or one already replaced, is left alone.
func TestWatchStartLeavesHealthyOrReplacedRuns(t *testing.T) {
	old := socketStartTimeout
	socketStartTimeout = 10 * time.Millisecond
	t.Cleanup(func() { socketStartTimeout = old })

	s := New(agentconfig.SlackChannelConfig{})
	runCtx, gen, done := s.beginRun(context.Background())
	defer done()
	s.setSocketState("connecting")
	s.watchStart(runCtx, gen)

	_, gen2, done2 := s.beginRun(context.Background())
	defer done2()
	_ = gen2
	s.setSocketState("starting")
	s.watchStart(runCtx, gen) // stale generation
	time.Sleep(30 * time.Millisecond)
	if state, _ := s.SocketState(); state != "starting" {
		t.Fatalf("state = %q, want untouched", state)
	}
}

// beginRun registers the run before any goroutine starts, so a Stop issued
// right after cancels THIS run and runWg waits for it.
func TestBeginRunIsVisibleToStopImmediately(t *testing.T) {
	s := New(agentconfig.SlackChannelConfig{})
	runCtx, _, done := s.beginRun(context.Background())
	s.Stop()
	if runCtx.Err() == nil {
		t.Fatal("Stop did not cancel the run registered just before it")
	}
	waited := make(chan struct{})
	go func() { s.runWg.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("runWg did not wait for the registered run")
	case <-time.After(20 * time.Millisecond):
	}
	done()
	<-waited
}

// A settings save reloads with its HTTP request's context, which ends right
// after the response: the restarted run must not end with it.
func TestReloadOutlivesTheCallersContext(t *testing.T) {
	cfg := agentconfig.SlackChannelConfig{Mode: "http", BotToken: "xoxb-test", SigningSecret: "s"}
	s := NewWithOwnerCached(cfg, "", "UBOT", "Bot", "Team")
	reqCtx, endRequest := context.WithCancel(context.Background())
	s.Reload(reqCtx, cfg, "")
	endRequest()
	time.Sleep(50 * time.Millisecond)
	s.runMu.Lock()
	cancel := s.runCancel
	s.runMu.Unlock()
	if cancel == nil {
		t.Fatal("reload did not start a run")
	}
	ended := make(chan struct{})
	go func() { s.runWg.Wait(); close(ended) }()
	select {
	case <-ended:
		t.Fatal("the run ended with the caller's request")
	case <-time.After(50 * time.Millisecond):
	}
	s.Stop()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not end the run")
	}
}
