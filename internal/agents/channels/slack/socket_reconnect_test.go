package slack

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack/socketmode"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// A socket client that gives up for good (here: a revoked token) is
// replaced by a fresh one after a backoff, so the bot reconnects on its own.
func TestServeReconnectsAfterTheSocketStops(t *testing.T) {
	oldMin, oldRunner := socketRetryMin, socketRunner
	socketRetryMin = 10 * time.Millisecond
	var runs atomic.Int32
	socketRunner = func(ctx context.Context, c *socketmode.Client) error {
		if runs.Add(1) == 1 {
			return errors.New("token_revoked")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { socketRetryMin, socketRunner = oldMin, oldRunner })

	cfg := agentconfig.SlackChannelConfig{BotToken: "xoxb-test", AppToken: "xapp-test"}
	s := NewWithOwnerCached(cfg, "", "UBOT", "Bot", "Team")
	first := s.socket
	served := make(chan error, 1)
	go func() { served <- s.Start(context.Background()) }()

	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("stopped socket was not replaced")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.cfgMu.Lock()
	replaced := s.socket != first
	s.cfgMu.Unlock()
	if !replaced {
		t.Fatal("reconnect reused the stopped client")
	}

	s.Stop()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Start returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not end the supervised run")
	}
}

// A credential failure shows as error, anything else as plainly offline.
func TestIsFatalSocketErr(t *testing.T) {
	for err, want := range map[error]bool{
		nil:                         false,
		errors.New("invalid_auth"):  true,
		errors.New("token_revoked"): true,
		errors.New("ping timeout"):  false,
	} {
		if got := isFatalSocketErr(err); got != want {
			t.Errorf("isFatalSocketErr(%v) = %v, want %v", err, got, want)
		}
	}
}
