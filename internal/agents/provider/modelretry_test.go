package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
)

// ompRefused is an omp turn refused its model before the agent ran (what
// rpc_turn.go emits for a failed prompt).
func ompRefused(model string) []string {
	return []string{
		`{"type":"message_start","message":{"role":"assistant","provider":"openai","model":"` + model + `","content":[]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"{\"error\":{\"code\":\"model_not_found\"}}","content":[]}}`,
		`{"type":"agent_end","messages":[]}`,
	}
}

func ompAnswer(model, text string) []string {
	return []string{
		`{"type":"message_start","message":{"role":"assistant","provider":"openai","model":"` + model + `","content":[]}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"` + text + `"}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[]}}`,
		`{"type":"agent_end","messages":[]}`,
	}
}

type retryRig struct {
	sp       *fakeSpawner
	a        *Agent
	convPath string
}

func newRetryRig(t *testing.T, on bool, lines ...[]string) *retryRig {
	t.Helper()
	withModelStateDir(t)
	RegisterModelSets(TypeOMP, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOMP, nil) })
	ins := &Instance{Type: TypeOMP, Name: "retry", AutoRetryModel: on,
		Models: []ModelEntry{{ID: "openai/a"}, {ID: "openai/b"}, {ID: "openai/c"}}}
	layout := config.NewLayout(t.TempDir())
	st := store.New(store.Options{Layout: layout, SessionID: "s1", AgentName: "main"})
	sp := &fakeSpawner{Lines: lines}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   5 * time.Second,
		ParserFactory: func() event.Parser { return event.NewOMPParser("omp") },
		Spawner:       sp,
		State:         state.New(nil),
		Store:         st,
		SendMode:      SendRespawnQueue,
		Instance:      ins,
		ModelID:       "openai/a",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return &retryRig{sp: sp, a: a, convPath: layout.SessionConversation("s1")}
}

// settle waits until n spawns happened and then a little longer, so a
// wrong extra retry would have shown up.
func (r *retryRig) settle(t *testing.T, n int) []SpawnOptions {
	t.Helper()
	waitFor(t, func() bool { return r.sp.callsSnapshot() >= n }, 3*time.Second)
	time.Sleep(400 * time.Millisecond)
	r.sp.mu.Lock()
	defer r.sp.mu.Unlock()
	var out []SpawnOptions
	for _, p := range r.sp.Procs {
		out = append(out, p.opt)
	}
	return out
}

func (r *retryRig) conversation() string {
	b, _ := os.ReadFile(r.convPath)
	return string(b)
}

func TestAutoRetryModelRerunsTurnOnNextModel(t *testing.T) {
	r := newRetryRig(t, true, ompRefused("a"), ompAnswer("b", "hello"))
	_ = r.a.Send("hi")
	got := r.settle(t, 2)
	if len(got) != 2 {
		t.Fatalf("want one retry, got %d spawns", len(got))
	}
	if got[1].InitialMessage != "hi" || got[1].ModelID != "openai/b" {
		t.Fatalf("retry = %q on %q, want the same message on openai/b", got[1].InitialMessage, got[1].ModelID)
	}
	if _, bad := ModelUnavailable(*r.a.cfg.Instance, "openai", "openai/a"); !bad {
		t.Fatal("the refused model must still be marked unavailable")
	}
	conv := r.conversation()
	if !strings.Contains(conv, "Model a is not available for this account — retried with b.") {
		t.Fatalf("missing retry note in transcript: %s", conv)
	}
	if strings.Contains(conv, "pilih model lain") {
		t.Fatalf("a retried turn must not also say pick another model: %s", conv)
	}
	// The agent stays on the model that worked for the next turn.
	r.a.mu.Lock()
	pin := r.a.spawnModelIDLocked()
	r.a.mu.Unlock()
	if pin != "openai/b" {
		t.Fatalf("next spawn pin = %q", pin)
	}
}

func TestAutoRetryModelOffDoesNotRetry(t *testing.T) {
	r := newRetryRig(t, false, ompRefused("a"), ompAnswer("b", "hello"))
	_ = r.a.Send("hi")
	if got := r.settle(t, 1); len(got) != 1 {
		t.Fatalf("off must not retry, got %d spawns", len(got))
	}
	waitFor(t, func() bool { return strings.Contains(r.conversation(), "pilih model lain") }, 2*time.Second)
}

func TestAutoRetryModelNotAfterOutput(t *testing.T) {
	produced := append(ompAnswer("a", "partial")[:2], ompRefused("a")[1:]...)
	r := newRetryRig(t, true, produced, ompAnswer("b", "hello"))
	_ = r.a.Send("hi")
	if got := r.settle(t, 1); len(got) != 1 {
		t.Fatalf("a turn that produced output must not be retried, got %d spawns", len(got))
	}
	// Not retried → the usual "pick another model" line is still posted.
	waitFor(t, func() bool { return strings.Contains(r.conversation(), "pilih model lain") }, 2*time.Second)
}

func TestAutoRetryModelStopsAfterTwoRetries(t *testing.T) {
	r := newRetryRig(t, true, ompRefused("a"), ompRefused("b"), ompRefused("c"), ompAnswer("a", "x"))
	_ = r.a.Send("hi")
	got := r.settle(t, 3)
	if len(got) != 3 {
		t.Fatalf("want 1 run + 2 retries, got %d spawns", len(got))
	}
	if got[1].ModelID != "openai/b" || got[2].ModelID != "openai/c" {
		t.Fatalf("retries ran on %q, %q", got[1].ModelID, got[2].ModelID)
	}
	waitFor(t, func() bool { return strings.Contains(r.conversation(), "pilih model lain") }, 2*time.Second)
}

func TestNextRetryModelPrefersLastWorkedAndSkipsRefused(t *testing.T) {
	withModelStateDir(t)
	ins := Instance{Type: TypeOMP, Name: "pick",
		Models: []ModelEntry{{ID: "openai/a"}, {ID: "openai/b"}, {ID: "openai/c"}}}
	if got := nextRetryModel(ins, "openai", "openai/a", nil); got != "openai/b" {
		t.Fatalf("no history: next listed = %q", got)
	}
	MarkModelWorked(ins, "", "openai/c")
	if got := nextRetryModel(ins, "openai", "openai/a", nil); got != "openai/c" {
		t.Fatalf("last worked first, got %q", got)
	}
	MarkModelUnavailable(ins, "openai", "openai/c", "")
	if got := nextRetryModel(ins, "openai", "openai/b", nil); got != "openai/a" {
		t.Fatalf("refused last-worked skipped, wrap to a, got %q", got)
	}
	if got := nextRetryModel(ins, "openai", "openai/b", []string{"openai/a"}); got != "" {
		t.Fatalf("nothing left, got %q", got)
	}
}

func TestAutoRetryModelConfigOnlyForOmpAndOpencode(t *testing.T) {
	has := func(typ Type) bool {
		for _, c := range SeedInstanceConfig(Instance{Type: typ, Name: "x"}) {
			if c.Key == "auto_retry_model" {
				return c.Value == "false"
			}
		}
		return false
	}
	if !has(TypeOMP) || !has(TypeOpencode) || has(TypeClaude) || has(TypeCodex) {
		t.Fatal("auto_retry_model must be offered (default off) on omp/opencode only")
	}
	var ins Instance
	ApplyInstanceConfigKey(&ins, "auto_retry_model", "true")
	if !ins.AutoRetryModel {
		t.Fatal("auto_retry_model=true must turn it on")
	}
	ins.Type = TypeClaude
	if autoRetryModelOn(&ins) {
		t.Fatal("claude never auto-retries")
	}
}
