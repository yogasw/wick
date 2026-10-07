package provider

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// ompResumeGoneLines is an omp RPC turn whose process never came up
// because --resume named a session omp does not have: the failure is the
// turn's own error, in the stream, and the process exits clean.
func ompResumeGoneLines(id string) []string {
	return []string{
		`{"type":"session","id":"","cwd":"/w"}`,
		`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"omp rpc exited before ready: Error: Session \"` + id + `\" not found. Run ` + "`omp --resume`" + ` without an argument."}}`,
		`{"type":"agent_end","messages":[]}`,
	}
}

// An in-band "Session … not found" clears the agent's resume id so the
// next respawn starts clean, and is reported to the pool exactly once.
func TestInBandResumeNotFoundClearsResume(t *testing.T) {
	sp := &fakeSpawner{Lines: [][]string{
		ompResumeGoneLines("sid-gone"),
		codexLines("", ""), // the next turn: any clean turn
	}}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewOMPParser("omp") },
		Spawner:       sp,
		State:         state.New(nil),
		SendMode:      SendRespawnQueue,
		ResumeID:      "sid-gone",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Send("hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sp.callsSnapshot() >= 1 && !a.Running() }, time.Second)
	if got := sp.procAt(0).opt.ResumeID; got != "sid-gone" {
		t.Fatalf("first spawn resume = %q", got)
	}
	waitFor(t, func() bool { return a.ResumeID() == "" }, time.Second)
	if !a.TakeResumeLost() {
		t.Fatal("resume loss not reported")
	}
	if a.TakeResumeLost() {
		t.Fatal("resume loss reported twice")
	}
	if err := a.Send("again"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sp.callsSnapshot() >= 2 }, time.Second)
	if got := sp.procAt(1).opt.ResumeID; got != "" {
		t.Fatalf("next spawn still resumes %q", got)
	}
}

// Any other turn error leaves the resume id alone.
func TestInBandOtherErrorKeepsResume(t *testing.T) {
	sp := &fakeSpawner{Lines: [][]string{{
		`{"type":"session","id":"sid-1","cwd":"/w"}`,
		`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"429 rate limited"}}`,
		`{"type":"agent_end","messages":[]}`,
	}}}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewOMPParser("omp") },
		Spawner:       sp,
		State:         state.New(nil),
		SendMode:      SendRespawnQueue,
		ResumeID:      "sid-1",
	})
	_ = a.Start(context.Background())
	_ = a.Send("hi")
	waitFor(t, func() bool { return sp.callsSnapshot() >= 1 && !a.Running() }, time.Second)
	if a.TakeResumeLost() || a.ResumeID() != "sid-1" {
		t.Fatalf("lost=%v resume=%q", a.TakeResumeLost(), a.ResumeID())
	}
}
