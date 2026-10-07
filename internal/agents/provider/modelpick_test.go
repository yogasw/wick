package provider

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// primeModels puts list in ins's CLI model cache, fresh, so nothing execs.
func primeModels(t *testing.T, ins Instance, ids ...string) {
	t.Helper()
	t.Cleanup(SetCLIModelsForTest(ins, ids...))
}

func pickIns(name string, live bool, def string) Instance {
	return Instance{Type: TypeOMP, Name: name, LiveModels: live, LiveModelDefault: def, OMPConfig: &OMPConfig{Profile: "wick-" + name}}
}

func TestExplicitLiveDefault(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	ctx := context.Background()
	ins := pickIns("pk-exp", true, "openai-codex/gpt-5.6-luna")
	primeModels(t, ins, "openai-codex/gpt-5.5", "openai-codex/gpt-5.6-luna")
	if got := ExplicitLiveDefault(ctx, ins); got != "openai-codex/gpt-5.6-luna" {
		t.Fatalf("chosen default: %q", got)
	}
	// Refused on this account: the next usable model, never the refused one.
	MarkModelUnavailable(ins, "openai-codex", "openai-codex/gpt-5.6-luna", "model_not_found")
	if got := ExplicitLiveDefault(ctx, ins); got != "openai-codex/gpt-5.5" {
		t.Fatalf("refused default: %q", got)
	}
	// No Default chosen, or live off: this rule says nothing.
	if got := ExplicitLiveDefault(ctx, pickIns("pk-exp", true, "")); got != "" {
		t.Fatalf("no default: %q", got)
	}
	if got := ExplicitLiveDefault(ctx, pickIns("pk-exp", false, "openai-codex/gpt-5.6-luna")); got != "" {
		t.Fatalf("live off: %q", got)
	}
}

func TestInstanceOwnModel(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	ctx := context.Background()
	live := pickIns("pk-own-live", true, "")
	primeModels(t, live, "openai-codex/a", "openai-codex/b")
	if got := InstanceOwnModel(ctx, live); got != "openai-codex/a" {
		t.Fatalf("live, no evidence: %q", got)
	}
	MarkModelWorked(live, "", "openai-codex/b")
	if got := InstanceOwnModel(ctx, live); got != "openai-codex/b" {
		t.Fatalf("live, last worked: %q", got)
	}
	off := pickIns("pk-own-off", false, "")
	primeModels(t, off, "openai-codex/x", "openai-codex/y")
	if got := InstanceOwnModel(ctx, off); got != "openai-codex/x" {
		t.Fatalf("live off, list: %q", got)
	}
	MarkModelWorked(off, "", "openai-codex/y")
	if got := InstanceOwnModel(ctx, off); got != "openai-codex/y" {
		t.Fatalf("live off, last worked: %q", got)
	}
}

// The evidence-only rule for an empty Default model is unchanged.
func TestProvenDefaultUnchangedWithoutChosenDefault(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	ins := pickIns("pk-proven", true, "")
	primeModels(t, ins, "openai-codex/a", "openai-codex/b")
	if got := ProvenDefaultModel(ins); got != "" {
		t.Fatalf("no evidence: %q, want the CLI's own default", got)
	}
	MarkModelWorked(ins, "", "openai-codex/b")
	if got := ProvenDefaultModel(ins); got != "openai-codex/b" {
		t.Fatalf("with evidence: %q", got)
	}
}

func TestStalePin(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	b := pickIns("pk-stale", true, "")
	cold := pickIns("pk-cold", true, "")
	primeModels(t, b, "openai-codex/gpt-5.6-luna")
	router := b
	router.UseAIRouter = true
	claude := Instance{Type: TypeClaude, Name: "claude"}
	cases := []struct {
		name  string
		ins   *Instance
		pin   string
		stale bool
	}{
		{"pin from A, not on B's list", &b, "openai-codex/gpt-5.5", true},
		{"pin on B's list", &b, "openai-codex/gpt-5.6-luna", false},
		{"cold cache: no evidence either way", &cold, "openai-codex/gpt-5.5", false},
		{"AI router ignores pins", &router, "openai-codex/gpt-5.5", false},
		{"wick-shaped pin is ModelArgs' to drop", &b, "m_0370951f-68d", false},
		{"not omp/opencode", &claude, "opus", false},
		{"no pin", &b, "", false},
	}
	for _, c := range cases {
		if _, got := StalePin(c.ins, c.pin); got != c.stale {
			t.Errorf("%s: stale=%v, want %v", c.name, got, c.stale)
		}
	}
	// Refused here since: stale even with a cold list.
	MarkModelUnavailable(cold, "openai-codex", "openai-codex/gpt-5.5", "model_not_found")
	if m, got := StalePin(&cold, "openai-codex/gpt-5.5"); !got || m != "openai-codex/gpt-5.5" {
		t.Fatalf("refused pin: %q %v", m, got)
	}
}

// A stale pin is dropped before the spawn: the CLI gets no --model pin,
// runs the instance default, and the session is told once.
func TestAgentDropsStalePin(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	ins := pickIns("pk-agent", true, "")
	primeModels(t, ins, "openai-codex/gpt-5.6-luna")
	sp := &fakeSpawner{Lines: [][]string{codexLines("s1", "hi"), codexLines("s1", "again")}}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewOMPParser("omp") },
		Spawner:       sp,
		State:         state.New(nil),
		SendMode:      SendRespawnQueue,
		Instance:      &ins,
		ModelID:       "openai-codex/gpt-5.5",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Send("hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sp.callsSnapshot() >= 1 }, time.Second)
	if got := sp.procAt(0).opt.ModelID; got != "" {
		t.Fatalf("stale pin reached the spawn: %q", got)
	}
}
