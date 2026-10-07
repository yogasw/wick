package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

func collectNotices(w *modelTurnWatch) chan string {
	ch := make(chan string, 8)
	w.notice = func(m string) { ch <- m }
	return ch
}

func nextNotice(t *testing.T, ch chan string) string {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no transcript notice")
		return ""
	}
}

func TestModelUnavailableNoticeNamesModelAndPlan(t *testing.T) {
	withModelStateDir(t)
	RegisterModelSets(TypeOMP, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOMP, nil) })
	prev := AccountPlanLabel
	AccountPlanLabel = func(_ Instance, prov, account string) string {
		if prov == "openai-codex" && account == "" {
			return "ChatGPT free"
		}
		return ""
	}
	t.Cleanup(func() { AccountPlanLabel = prev })

	ins := &Instance{Type: TypeOMP, Name: "yoga"}
	w := newModelTurnWatch(ins, "")
	ch := collectNotices(w)
	w.observe(`{"type":"message_start","message":{"provider":"openai-codex","model":"gpt-5.5"}}`)
	w.observe(`{"type":"message_end","message":{"stopReason":"error","errorMessage":"{\"error\":{\"code\":\"model_not_found\"}}"}}`)
	w.observe(`{"type":"message_end","message":{"stopReason":"error","errorMessage":"model_not_found again"}}`)
	if got := nextNotice(t, ch); got != "gpt-5.5 tidak tersedia untuk akun ChatGPT free ini — pilih model lain." {
		t.Fatalf("notice = %q", got)
	}
	select {
	case m := <-ch:
		t.Fatalf("one notice per turn, got a second: %q", m)
	case <-time.After(100 * time.Millisecond):
	}

	// opencode: no plan known → the plain wording.
	RegisterModelSets(TypeOpencode, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOpencode, nil) })
	oc := &Instance{Type: TypeOpencode, Name: "oc", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()}}
	w = newModelTurnWatch(oc, EncodePin([]string{"openai"}, "openai/gpt-5.5"))
	ch = collectNotices(w)
	w.observe(`{"type":"error","error":{"data":{"message":"You do not have access to the model gpt-5.5"}}}`)
	if got := nextNotice(t, ch); got != "gpt-5.5 tidak tersedia untuk akun ini — pilih model lain." {
		t.Fatalf("opencode notice = %q", got)
	}
}

func writeOpencodeAuth(t *testing.T, ins Instance, providers ...string) {
	t.Helper()
	f, err := OpencodeAuthFile(ins)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Dir(f), 0o700)
	m := map[string]any{}
	for _, p := range providers {
		m[p] = map[string]string{"type": "api"}
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOpencodeQuotaRotatesAndRetriesOnce(t *testing.T) {
	withModelStateDir(t)
	RegisterModelSets(TypeOpencode, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOpencode, nil) })
	ins := &Instance{Type: TypeOpencode, Name: "rot-retry", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()}}
	writeOpencodeAuth(t, *ins, "openai")
	_, a2, err := NewOpencodeAccount(*ins)
	if err != nil {
		t.Fatal(err)
	}
	writeOpencodeAuth(t, a2, "openai")
	pin := EncodePin([]string{"openai", "auto"}, "openai/gpt-5")
	quota := `{"type":"error","error":{"data":{"message":"429 Too Many Requests: usage limit reached"}}}`

	retries := 0
	w := newModelTurnWatch(ins, pin)
	ch := collectNotices(w)
	w.rotate = func() bool { retries++; return retries == 1 }
	w.observe(quota)
	w.observe(quota) // same turn: no second notice / retry
	if got := nextNotice(t, ch); got != "Akun main (openai) kena limit → pindah ke akun a2; pesan ini diulang sekali di sana." {
		t.Fatalf("notice = %q", got)
	}
	if retries != 1 {
		t.Fatalf("retries = %d", retries)
	}
	if _, acct := OpencodeSpawnAccount(*ins, pin); acct != "a2" {
		t.Fatalf("retry must run on a2, got %q", acct)
	}

	// The retry on a2 also hits the limit: main is still exhausted, no
	// account left → no further retry.
	w = newModelTurnWatch(ins, pin)
	ch = collectNotices(w)
	w.rotate = func() bool { retries++; return retries == 1 }
	w.observe(quota)
	if got := nextNotice(t, ch); got != "Akun a2 (openai) kena limit → turn berikutnya memakai akun main." {
		t.Fatalf("second notice = %q", got)
	}

	// A pinned account is never rotated away from.
	pinned := EncodePin([]string{"openai", "a2"}, "openai/gpt-5")
	w = newModelTurnWatch(ins, pinned)
	ch = collectNotices(w)
	w.rotate = func() bool { t.Fatal("pinned account must not retry"); return false }
	w.observe(quota)
	if got := nextNotice(t, ch); got != "Akun a2 (openai) kena limit — akun ini dipilih manual, jadi tidak dipindah. Pilih akun lain atau Auto." {
		t.Fatalf("pinned notice = %q", got)
	}
}

func TestAgentRequeueForRetryOncePerMessage(t *testing.T) {
	a := &Agent{pendingQueue: []string{"later"}}
	if !a.requeueForRetry("hi") || len(a.pendingQueue) != 2 || a.pendingQueue[0] != "hi" {
		t.Fatalf("retry must run first: %v", a.pendingQueue)
	}
	if a.requeueForRetry("hi") {
		t.Fatal("the same message must be retried only once")
	}
	// drainPending joins the queue, so the re-run turn is "hi\n\nlater".
	if a.requeueForRetry(joinQueued(a.pendingQueue)) {
		t.Fatal("the re-run turn joined with queued messages must not retry again")
	}
	if !a.requeueForRetry("other") {
		t.Fatal("a new message may be retried")
	}
	a.stopped = true
	if a.requeueForRetry("x") {
		t.Fatal("a stopped agent must not requeue")
	}
}

// Through a real Agent + opencode parser: a turn that hits the quota on
// Auto runs again ONCE (next account), and a retry that hits it again
// does not loop.
func TestAgentRetriesQuotaTurnOnceOnNextAccount(t *testing.T) {
	withModelStateDir(t)
	RegisterModelSets(TypeOpencode, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOpencode, nil) })
	ins := &Instance{Type: TypeOpencode, Name: "rot-agent", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()}}
	writeOpencodeAuth(t, *ins, "openai")
	_, a2, _ := NewOpencodeAccount(*ins)
	writeOpencodeAuth(t, a2, "openai")

	sp := newGatedSpawner()
	close(sp.release)
	sp.start = []string{`{"type":"step_start","sessionID":"ses_1","part":{"type":"step-start"}}`}
	sp.end = []string{`{"type":"error","sessionID":"ses_1","error":{"name":"APIError","data":{"message":"429 Too Many Requests: usage limit reached"}}}`}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   5 * time.Second,
		ParserFactory: func() event.Parser { return event.NewOpencodeParser("opencode") },
		Spawner:       sp,
		State:         state.New(nil),
		SendMode:      SendRespawnQueue,
		Instance:      ins,
		ModelID:       EncodePin([]string{"openai", "auto"}, "openai/gpt-5"),
	})
	_ = a.Start(context.Background())
	defer a.Stop()
	_ = a.Send("hi")
	waitFor(t, func() bool { return len(sp.spawns()) == 2 }, 3*time.Second)
	time.Sleep(500 * time.Millisecond)
	got := sp.spawns()
	if len(got) != 2 || got[1].InitialMessage != "hi" {
		t.Fatalf("want the quota turn re-run once with the same message, got %d spawns", len(got))
	}
}
