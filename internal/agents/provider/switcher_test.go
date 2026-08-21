package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

// nopPool satisfies the Pool interface without doing any real work — Switch
// only needs Kill (called async) and Send (only on Greeting, unused here).
type nopPool struct{}

func (nopPool) Kill(sessionID, agentName string) error { return nil }
func (nopPool) Send(ctx context.Context, sessionID, agentName, source, role, text string) error {
	return nil
}

// seedSession creates sessions/<id>/ with a single agent on the given provider.
func seedSession(t *testing.T, layout config.Layout, id, agentName, prov string) {
	t.Helper()
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: id}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AddAgent(layout, id, agentName, prov); err != nil {
		t.Fatalf("add agent: %v", err)
	}
}

func TestSwitch(t *testing.T) {
	isolateConfig(t)
	// Seed a default codex instance, a named one, and a default claude. Each
	// seed drains Save's async probe write (saveSeed) so the three seeds — and
	// the switches below — don't race each other's config.json rewrite.
	saveSeed(t, Instance{Type: TypeCodex, Name: "codex"})
	saveSeed(t, Instance{Type: TypeCodex, Name: "gemini_flash"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "claude"})
	saveSeed(t, Instance{Type: TypeWick, Name: "wick"})
	layout := config.NewLayout(t.TempDir())

	t.Run("switches to a named instance and persists type/name", func(t *testing.T) {
		seedSession(t, layout, "sess-named", "main", "codex/codex")
		if err := Switch(layout, nopPool{}, "sess-named", "main", "codex/gemini_flash", SwitchOptions{}); err != nil {
			t.Fatalf("switch: %v", err)
		}
		loaded, err := session.Load(layout, "sess-named")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got := loaded.Agents[0].Provider; got != "codex/gemini_flash" {
			t.Fatalf("provider = %q, want codex/gemini_flash", got)
		}
	})

	t.Run("rejects switching to the provider already active", func(t *testing.T) {
		seedSession(t, layout, "sess-same", "main", "codex/codex")
		// Bare "codex" normalizes to codex/codex — the current provider.
		err := Switch(layout, nopPool{}, "sess-same", "main", "codex", SwitchOptions{})
		if err == nil {
			t.Fatal("want error switching to current provider")
		}
		if !strings.Contains(err.Error(), "already using") {
			t.Fatalf("error = %v, want 'already using'", err)
		}
	})

	t.Run("rejects an unknown provider", func(t *testing.T) {
		seedSession(t, layout, "sess-unknown", "main", "codex/codex")
		err := Switch(layout, nopPool{}, "sess-unknown", "main", "codex/nope", SwitchOptions{})
		if err == nil || !strings.Contains(err.Error(), "unknown provider") {
			t.Fatalf("error = %v, want 'unknown provider'", err)
		}
	})

	t.Run("back-to-back switches collapse to the last one", func(t *testing.T) {
		seedSession(t, layout, "sess-chain", "main", "codex/codex")
		convPath := layout.SessionConversation("sess-chain")
		// codex → gemini_flash → claude, no chat in between.
		if err := Switch(layout, nopPool{}, "sess-chain", "main", "codex/gemini_flash", SwitchOptions{}); err != nil {
			t.Fatalf("switch 1: %v", err)
		}
		if err := Switch(layout, nopPool{}, "sess-chain", "main", "claude", SwitchOptions{}); err != nil {
			t.Fatalf("switch 2: %v", err)
		}
		turns := readConv(t, convPath)
		// Only the final switch's single system turn remains.
		if len(turns) != 1 {
			t.Fatalf("turns = %d, want 1 (only last switch); got %+v", len(turns), turns)
		}
		if turns[0].Kind != store.KindProviderSwitch {
			t.Fatalf("kind = %q, want provider_switch", turns[0].Kind)
		}
		if got := turns[0].Extras["to"]; got != "claude/claude" {
			t.Fatalf("extras.to = %q, want claude/claude", got)
		}
	})

	t.Run("a real chat between switches is preserved", func(t *testing.T) {
		seedSession(t, layout, "sess-keep", "main", "codex/codex")
		convPath := layout.SessionConversation("sess-keep")
		if err := Switch(layout, nopPool{}, "sess-keep", "main", "codex/gemini_flash", SwitchOptions{}); err != nil {
			t.Fatalf("switch 1: %v", err)
		}
		// Simulate a real user + assistant exchange after the first switch.
		_ = storage.AppendJSONL(convPath, "wick-conv-v1", "sess-keep", store.ConversationTurn{Role: "user", Text: "hello"})
		_ = storage.AppendJSONL(convPath, "wick-conv-v1", "sess-keep", store.ConversationTurn{Role: "assistant", Text: "hi there"})
		if err := Switch(layout, nopPool{}, "sess-keep", "main", "claude", SwitchOptions{}); err != nil {
			t.Fatalf("switch 2: %v", err)
		}
		turns := readConv(t, convPath)
		// switch1 (1) + chat (2) + switch2 (1) — nothing pruned, the chat broke the run.
		if len(turns) != 4 {
			t.Fatalf("turns = %d, want 4; got %+v", len(turns), turns)
		}
	})

	t.Run("pins a model id when switching to wick", func(t *testing.T) {
		seedSession(t, layout, "sess-wick-pin", "main", "codex/codex")
		if err := Switch(layout, nopPool{}, "sess-wick-pin", "main", "wick/wick", SwitchOptions{ModelID: "m_abc"}); err != nil {
			t.Fatalf("switch: %v", err)
		}
		loaded, err := session.Load(layout, "sess-wick-pin")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got := loaded.Agents[0].ModelID; got != "m_abc" {
			t.Fatalf("ModelID = %q, want m_abc", got)
		}
	})

	t.Run("clears the model pin when switching away from wick", func(t *testing.T) {
		seedSession(t, layout, "sess-wick-clear", "main", "wick/wick")
		if err := session.SetModelID(layout, "sess-wick-clear", "main", "m_old"); err != nil {
			t.Fatalf("seed pin: %v", err)
		}
		if err := Switch(layout, nopPool{}, "sess-wick-clear", "main", "claude", SwitchOptions{}); err != nil {
			t.Fatalf("switch: %v", err)
		}
		loaded, err := session.Load(layout, "sess-wick-clear")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got := loaded.Agents[0].ModelID; got != "" {
			t.Fatalf("ModelID = %q, want cleared", got)
		}
	})

	t.Run("picking a different model on the same wick instance is not a no-op", func(t *testing.T) {
		seedSession(t, layout, "sess-wick-remodel", "main", "wick/wick")
		if err := session.SetModelID(layout, "sess-wick-remodel", "main", "m_1"); err != nil {
			t.Fatalf("seed pin: %v", err)
		}
		// Same provider tag, different model — must succeed, not "already using".
		if err := Switch(layout, nopPool{}, "sess-wick-remodel", "main", "wick/wick", SwitchOptions{ModelID: "m_2"}); err != nil {
			t.Fatalf("switch to different model on same instance: %v", err)
		}
		loaded, err := session.Load(layout, "sess-wick-remodel")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got := loaded.Agents[0].ModelID; got != "m_2" {
			t.Fatalf("ModelID = %q, want m_2", got)
		}
	})

	t.Run("same instance and same model pin is rejected as a no-op", func(t *testing.T) {
		seedSession(t, layout, "sess-wick-noop", "main", "wick/wick")
		if err := session.SetModelID(layout, "sess-wick-noop", "main", "m_1"); err != nil {
			t.Fatalf("seed pin: %v", err)
		}
		err := Switch(layout, nopPool{}, "sess-wick-noop", "main", "wick/wick", SwitchOptions{ModelID: "m_1"})
		if err == nil || !strings.Contains(err.Error(), "already using") {
			t.Fatalf("error = %v, want 'already using'", err)
		}
	})
}

// readConv returns every conversation turn on disk, in order.
func readConv(t *testing.T, path string) []store.ConversationTurn {
	t.Helper()
	var turns []store.ConversationTurn
	if err := storage.ReadJSONL(path, func(line []byte) bool {
		var ct store.ConversationTurn
		if err := json.Unmarshal(line, &ct); err != nil {
			t.Fatalf("decode turn: %v", err)
		}
		turns = append(turns, ct)
		return true
	}); err != nil {
		t.Fatalf("read conv: %v", err)
	}
	return turns
}

// TestSwitchResumeScope pins how resume ids travel across switches: shared
// within one provider type, isolated across types, and legacy key shapes
// still readable. Getting this wrong is not cosmetic — a wrong id is a
// spawn that dies with "No conversation found", and a blanked id abandons
// a conversation still on disk.
func TestSwitchResumeScope(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeClaude, Name: "claude"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "work"})
	saveSeed(t, Instance{Type: TypeCodex, Name: "codex"})
	saveSeed(t, Instance{Type: TypeWick, Name: "wick"})
	layout := config.NewLayout(t.TempDir())

	setCLIID := func(t *testing.T, sid, id string) {
		t.Helper()
		if err := session.SetCLISessionID(layout, sid, "main", id); err != nil {
			t.Fatal(err)
		}
	}
	agent := func(t *testing.T, sid string) session.AgentEntry {
		t.Helper()
		loaded, err := session.Load(layout, sid)
		if err != nil {
			t.Fatal(err)
		}
		return loaded.Agents[0]
	}

	t.Run("same type keeps the id across instances", func(t *testing.T) {
		seedSession(t, layout, "sc-same", "main", "claude/claude")
		setCLIID(t, "sc-same", "id-A")
		if err := Switch(layout, nopPool{}, "sc-same", "main", "claude/work", SwitchOptions{}); err != nil {
			t.Fatalf("switch: %v", err)
		}
		if a := agent(t, "sc-same"); a.CLISessionID != "id-A" {
			t.Errorf("CLISessionID = %q, want id-A — instances of one type share the transcript store", a.CLISessionID)
		}
	})

	t.Run("cross type starts fresh and parks the old id", func(t *testing.T) {
		seedSession(t, layout, "sc-cross", "main", "claude/claude")
		setCLIID(t, "sc-cross", "id-A")
		if err := Switch(layout, nopPool{}, "sc-cross", "main", "codex", SwitchOptions{}); err != nil {
			t.Fatalf("switch: %v", err)
		}
		a := agent(t, "sc-cross")
		if a.CLISessionID != "" {
			t.Errorf("CLISessionID = %q, want empty — codex cannot read claude's store", a.CLISessionID)
		}
		if a.ProviderSessions["claude"] != "id-A" {
			t.Errorf("ProviderSessions[claude] = %q, want id-A — the id must survive the switch", a.ProviderSessions["claude"])
		}
	})

	t.Run("switching back restores the parked id", func(t *testing.T) {
		seedSession(t, layout, "sc-back", "main", "claude/claude")
		setCLIID(t, "sc-back", "id-A")
		if err := Switch(layout, nopPool{}, "sc-back", "main", "codex", SwitchOptions{}); err != nil {
			t.Fatalf("switch to codex: %v", err)
		}
		setCLIID(t, "sc-back", "id-C") // codex ran and recorded its own
		if err := Switch(layout, nopPool{}, "sc-back", "main", "claude/claude", SwitchOptions{}); err != nil {
			t.Fatalf("switch back: %v", err)
		}
		a := agent(t, "sc-back")
		if a.CLISessionID != "id-A" {
			t.Errorf("CLISessionID = %q, want id-A — claude must resume its own conversation", a.CLISessionID)
		}
		if a.ProviderSessions["codex"] != "id-C" {
			t.Errorf("ProviderSessions[codex] = %q, want id-C", a.ProviderSessions["codex"])
		}
	})

	t.Run("legacy instance-shaped keys are still found", func(t *testing.T) {
		seedSession(t, layout, "sc-legacy", "main", "codex/codex")
		loaded, err := session.Load(layout, "sc-legacy")
		if err != nil {
			t.Fatal(err)
		}
		// A file written before ids were scoped by type.
		loaded.Agents[0].ProviderSessions = map[string]string{"claude/claude_default": "id-old"}
		if err := session.SaveAgents(layout, "sc-legacy", loaded.Agents); err != nil {
			t.Fatal(err)
		}
		if err := Switch(layout, nopPool{}, "sc-legacy", "main", "claude/claude", SwitchOptions{}); err != nil {
			t.Fatalf("switch: %v", err)
		}
		if a := agent(t, "sc-legacy"); a.CLISessionID != "id-old" {
			t.Errorf("CLISessionID = %q, want id-old — pre-scope keys must stay readable", a.CLISessionID)
		}
	})
}
