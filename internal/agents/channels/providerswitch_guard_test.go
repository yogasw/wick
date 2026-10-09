package channels

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
)

type noSwitchPool struct{ t *testing.T }

func (p noSwitchPool) Kill(string, string) error {
	p.t.Fatal("a refused switch killed the agent")
	return nil
}

func (p noSwitchPool) Send(context.Context, string, string, string, string, string) error {
	p.t.Fatal("a refused switch sent through the pool")
	return nil
}

// A "#tag" the guard refuses is ignored: the channel is told why and the
// body still goes out, untagged, on the current provider.
func TestWrapSendFuncRefusedSwitchStillSends(t *testing.T) {
	prev := provider.SwitchGuard
	provider.SwitchGuard = func(_ context.Context, _, tag string) error {
		return errors.New("provider is set by the agent")
	}
	t.Cleanup(func() { provider.SwitchGuard = prev })

	var sent, replies []string
	send := WrapSendFunc(func(_ context.Context, _, _, _, _, text string) error {
		sent = append(sent, text)
		return nil
	}, agentconfig.NewLayout(t.TempDir()), noSwitchPool{t}, func(_, _, _, text string) {
		replies = append(replies, text)
	})

	if err := send(context.Background(), "s1", "a", "slack", "user", "#codex hello there"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != "hello there" {
		t.Fatalf("sent = %q, want the body on the current provider", sent)
	}
	if len(replies) != 1 || !strings.Contains(replies[0], "Provider switch ignored: provider is set by the agent") {
		t.Fatalf("replies = %q", replies)
	}

	// Switch only: nothing to send, the notice alone.
	sent, replies = nil, nil
	if err := send(context.Background(), "s1", "a", "slack", "user", "#codex"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 0 || len(replies) != 1 {
		t.Fatalf("switch-only: sent %q, replies %q", sent, replies)
	}
}
