package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

func pinEntry(t *testing.T, layout config.Layout, id string) session.AgentEntry {
	t.Helper()
	s, err := session.Load(layout, id)
	if err != nil || len(s.Agents) != 1 {
		t.Fatalf("load %s: %v %+v", id, err, s.Agents)
	}
	return s.Agents[0]
}

func TestPinSession(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeClaude, Name: "claude"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "alt"})
	layout := config.NewLayout(t.TempDir())
	ctx := context.Background()
	prev := SessionDefault
	t.Cleanup(func() { SessionDefault = prev })

	t.Run("a session with no agent yet gets a pinned entry", func(t *testing.T) {
		if _, err := session.Create(ctx, layout, session.CreateOptions{ID: "s-new"}); err != nil {
			t.Fatal(err)
		}
		res, err := PinSession(ctx, layout, "s-new", "", "claude/alt", "big", SwitchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		a := pinEntry(t, layout, "s-new")
		if a.Name != "default" || a.Provider != "claude/alt" || a.ModelID != "big" || !a.Pinned {
			t.Fatalf("entry = %+v", a)
		}
		if a.Failover == nil || a.Failover.HomeProvider != "claude/alt" || a.Failover.HomeModelID != "big" {
			t.Fatalf("home = %+v, want claude/alt big", a.Failover)
		}
		if !res.Pinned || !res.Switched {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("an existing agent is switched and pinned in one write", func(t *testing.T) {
		seedSession(t, layout, "s-switch", "main", "claude/claude")
		if _, err := PinSession(ctx, layout, "s-switch", "", "claude/alt", "big", SwitchOptions{}); err != nil {
			t.Fatal(err)
		}
		if a := pinEntry(t, layout, "s-switch"); a.Provider != "claude/alt" || a.ModelID != "big" || !a.Pinned {
			t.Fatalf("entry = %+v", a)
		}
	})

	t.Run("pinning the pair already running only sets the pin", func(t *testing.T) {
		seedSession(t, layout, "s-same", "main", "claude/claude")
		res, err := PinSession(ctx, layout, "s-same", "", "claude", "", SwitchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if a := pinEntry(t, layout, "s-same"); a.Provider != "claude/claude" || !a.Pinned {
			t.Fatalf("entry = %+v", a)
		}
		if res.Switched {
			t.Fatal("same pair reported as a switch")
		}
	})

	t.Run("an unknown provider is refused and nothing is pinned", func(t *testing.T) {
		seedSession(t, layout, "s-bad", "main", "claude/claude")
		if _, err := PinSession(ctx, layout, "s-bad", "", "claude/nope", "", SwitchOptions{}); err == nil || !strings.Contains(err.Error(), "unknown provider") {
			t.Fatalf("err = %v, want unknown provider", err)
		}
		if a := pinEntry(t, layout, "s-bad"); a.Pinned {
			t.Fatal("refused pin was written")
		}
		if _, err := PinSession(ctx, layout, "s-bad", "", "", "", SwitchOptions{}); err == nil {
			t.Fatal("want an error without a provider")
		}
	})

	t.Run("clear goes back to the session default, unpinned", func(t *testing.T) {
		SessionDefault = func(context.Context, string) (string, string, bool) { return "claude", "small", true }
		seedSession(t, layout, "s-clear", "main", "claude/claude")
		if _, err := PinSession(ctx, layout, "s-clear", "", "claude/alt", "big", SwitchOptions{}); err != nil {
			t.Fatal(err)
		}
		res, err := UnpinSession(ctx, layout, "s-clear", "", SwitchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		a := pinEntry(t, layout, "s-clear")
		if a.Provider != "claude/claude" || a.ModelID != "small" || a.Pinned {
			t.Fatalf("entry = %+v, want claude/claude small unpinned", a)
		}
		if !res.Switched || res.Provider != "claude/claude" || res.Model != "small" {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("clear with no default keeps the pair, unpinned", func(t *testing.T) {
		SessionDefault = nil
		seedSession(t, layout, "s-nodef", "main", "claude/claude")
		if _, err := PinSession(ctx, layout, "s-nodef", "", "claude/alt", "big", SwitchOptions{}); err != nil {
			t.Fatal(err)
		}
		res, err := UnpinSession(ctx, layout, "s-nodef", "", SwitchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if a := pinEntry(t, layout, "s-nodef"); a.Provider != "claude/alt" || a.ModelID != "big" || a.Pinned {
			t.Fatalf("entry = %+v, want claude/alt big unpinned", a)
		}
		if res.Switched {
			t.Fatal("no default, but reported a switch")
		}
	})

	t.Run("the guard refuses a pin", func(t *testing.T) {
		prevG := FailoverGuard
		FailoverGuard = func(context.Context, string, string) error { return errDenied }
		t.Cleanup(func() { FailoverGuard = prevG })
		seedSession(t, layout, "s-guard", "main", "claude/claude")
		if _, err := PinSession(ctx, layout, "s-guard", "", "claude/alt", "", SwitchOptions{}); err != errDenied {
			t.Fatalf("err = %v, want the guard's", err)
		}
	})
}

var errDenied = errString("no access")

type errString string

func (e errString) Error() string { return string(e) }
