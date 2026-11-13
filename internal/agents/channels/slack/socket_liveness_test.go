package slack

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// A Reload must not wait forever on a run stuck in a handler: the bot would
// stay offline (state "disconnected") until the handler happened to return.
func TestReloadDoesNotWaitForAStuckRun(t *testing.T) {
	old := reloadWaitTimeout
	reloadWaitTimeout = 30 * time.Millisecond
	t.Cleanup(func() { reloadWaitTimeout = old })

	cfg := agentconfig.SlackChannelConfig{Mode: "http", BotToken: "xoxb-test", SigningSecret: "s"}
	s := NewWithOwnerCached(cfg, "", "UBOT", "Bot", "Team")
	stuckCtx, stuckGen, stuckDone := s.beginRun(context.Background())
	defer stuckDone()

	returned := make(chan struct{})
	go func() {
		s.Reload(context.Background(), cfg, "")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Reload blocked on the stuck run")
	}
	if stuckCtx.Err() == nil {
		t.Fatal("the stuck run was not cancelled")
	}
	if s.currentRun() == stuckGen {
		t.Fatal("Reload did not start a new run")
	}
	s.Stop()
}

// A socket that stays away from "connected" is restarted by the liveness
// watchdog.
func TestWatchConnectedRestartsADownSocket(t *testing.T) {
	setLiveness(t, 5*time.Millisecond, 20*time.Millisecond)

	s := New(agentconfig.SlackChannelConfig{})
	runCtx, gen, done := s.beginRun(context.Background())
	defer done()
	s.setSocketState("disconnected")

	finished := make(chan struct{})
	go func() { s.watchConnected(runCtx, gen); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not act on a down socket")
	}
	deadline := time.Now().Add(2 * time.Second)
	for runCtx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("down socket's run was not restarted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A connected socket, even one that blipped shorter than the threshold, is
// left alone.
func TestWatchConnectedLeavesAConnectedSocket(t *testing.T) {
	setLiveness(t, 5*time.Millisecond, 200*time.Millisecond)

	s := New(agentconfig.SlackChannelConfig{})
	runCtx, gen, done := s.beginRun(context.Background())
	defer done()
	s.setSocketState("connected")

	ctx, cancel := context.WithCancel(runCtx)
	finished := make(chan struct{})
	go func() { s.watchConnected(ctx, gen); close(finished) }()
	time.Sleep(30 * time.Millisecond)
	s.setSocketState("connecting") // a short blip
	time.Sleep(30 * time.Millisecond)
	s.setSocketState("connected")
	time.Sleep(250 * time.Millisecond)
	if runCtx.Err() != nil || s.currentRun() != gen {
		t.Fatal("watchdog restarted a healthy socket")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("watchdog did not stop with its run")
	}
}

// Events API events never block the socket loop, even when nothing is
// draining the queue.
func TestDispatchSocketEventNeverBlocks(t *testing.T) {
	s := New(agentconfig.SlackChannelConfig{})
	q := make(chan slackevents.EventsAPIEvent) // unbuffered, no reader
	evt := socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{Type: "unknown"}}

	returned := make(chan struct{})
	go func() {
		s.dispatchSocketEvent(context.Background(), evt, q)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("dispatch blocked the socket loop")
	}
}

// The worker handles queued events in order and exits once stopped.
func TestEventWorkerDrainsAndStops(t *testing.T) {
	s := New(agentconfig.SlackChannelConfig{})
	q, stop := s.startEventWorker(context.Background())
	for i := 0; i < 3; i++ {
		q <- slackevents.EventsAPIEvent{Type: "unknown"}
	}
	stop()
	stop() // idempotent
}

// Every Web API call is bounded on the connection and the response headers.
func TestSlackHTTPClientIsBounded(t *testing.T) {
	tr, ok := slackHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", slackHTTPClient.Transport)
	}
	if tr.ResponseHeaderTimeout <= 0 || tr.TLSHandshakeTimeout <= 0 {
		t.Fatalf("unbounded transport: header=%v tls=%v", tr.ResponseHeaderTimeout, tr.TLSHandshakeTimeout)
	}
}

func setLiveness(t *testing.T, tick, stuck time.Duration) {
	t.Helper()
	oldTick, oldStuck := socketLivenessTick, socketStuckAfter
	socketLivenessTick, socketStuckAfter = tick, stuck
	t.Cleanup(func() { socketLivenessTick, socketStuckAfter = oldTick, oldStuck })
}
