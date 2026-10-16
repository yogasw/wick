package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// Stop retires an instance: its webhook drops events, a Reload (config
// change, watchdog) does not bring it back, only Start does. A live
// instance keeps serving across a Reload.
func TestHTTPHandlerHaltedOnlyByStop(t *testing.T) {
	cfg := agentconfig.SlackChannelConfig{BotToken: "xoxb-1", Mode: "http", SigningSecret: "sig"}
	ch := NewWithOwnerCached(cfg, "", "UBOT", "bot", "T")
	ignored := func() bool {
		w := httptest.NewRecorder()
		ch.HTTPHandler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/integrations/slack/events", strings.NewReader(`{"type":"event_callback"}`)))
		return w.Header().Get("X-Wick-Ignored") != ""
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ignored() || !ch.Serving() {
		t.Fatal("a fresh instance must serve")
	}
	ch.Reload(ctx, cfg, "")
	if ignored() || !ch.Serving() {
		t.Fatal("a Reload must not stop a live instance serving")
	}
	ch.Stop()
	if !ignored() || ch.Serving() {
		t.Fatal("a stopped instance must drop events")
	}
	ch.Reload(ctx, cfg, "")
	if !ignored() {
		t.Fatal("a Reload brought a retired instance back")
	}
	go func() { _ = ch.Start(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for !ch.Serving() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ignored() {
		t.Fatal("Start must serve again")
	}
	ch.Stop()
}

// A Reload (hot-reload, watchdog) landing after Stop takes the config but
// does not start a run: a switched-off socket instance stays down.
func TestReloadDoesNotRestartStoppedSocketInstance(t *testing.T) {
	cfg := agentconfig.SlackChannelConfig{BotToken: "xoxb-1", Mode: "socket", AppToken: "xapp-1"}
	ch := NewWithOwnerCached(cfg, "", "UBOT", "bot", "T")
	ch.Stop()
	ch.runMu.Lock()
	gen := ch.runGen
	ch.runMu.Unlock()
	ch.Reload(context.Background(), cfg, "")
	ch.runMu.Lock()
	after := ch.runGen
	ch.runMu.Unlock()
	if after != gen {
		t.Fatalf("Reload started a run on a stopped instance (gen %d -> %d)", gen, after)
	}
	if st, _ := ch.SocketState(); st != "disconnected" {
		t.Fatalf("socket state after Reload = %q, want disconnected", st)
	}
}
