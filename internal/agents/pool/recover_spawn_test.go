package pool

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// TestRecoverTurnSpawnsAlone is the counterpart to
// TestSystemTurnDoesNotSpawnAlone: a recover notice (source "recover",
// role "system") has NO user turn following it — the agent just died and
// nobody is typing. If the non-user guard in send() swallows it, the
// notice sits buffered until a human happens to message the session, and
// crash auto-restart never fires. The recover source must spawn on its
// own, exactly once, with the notice in the prompt.
func TestRecoverTurnSpawnsAlone(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.recoverFromExit("S1", "default", provider.ExitError, "")
	waitFor(t, func() bool { return sp.procCount() == 1 && p.Active() == 0 }, 3*time.Second)

	if n := sp.procCount(); n != 1 {
		t.Fatalf("expected exactly 1 spawn from the recover turn, got %d", n)
	}
	stdin := sp.procAt(0).recordedStdin()
	if !contains(stdin, "stopped unexpectedly") {
		t.Fatalf("spawned prompt missing the crash notice: %q", stdin)
	}
}

// A host-OOM kill arrives as a retryable ExitError, but the agent must
// still be told the cause was memory: the generic "unexpected exit,
// carry on" notice tells it to repeat the exact allocation that got it
// killed. The exit's ReasonDetail (HostOOMDetail) must reach the notice.
func TestRecoverNoticeCarriesHostOOMDetail(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.recoverFromExit("S1", "default", provider.ExitError, provider.HostOOMDetail(1<<30))
	waitFor(t, func() bool { return sp.procCount() == 1 && p.Active() == 0 }, 3*time.Second)

	if n := sp.procCount(); n != 1 {
		t.Fatalf("expected exactly 1 spawn, got %d", n)
	}
	stdin := sp.procAt(0).recordedStdin()
	if !contains(stdin, "machine ran out of memory") {
		t.Fatalf("notice does not name the memory cause: %q", stdin)
	}
}

// An OOM exit is not retried, but the notice must still reach the agent —
// which means it must spawn (--resume) and deliver the oomNotice on its
// own, with no user turn coming.
func TestRecoverOOMNoticeSpawnsAlone(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.recoverFromExit("S1", "default", provider.ExitOOM, "used 2.0 GB, over its 1024 MB limit.")
	waitFor(t, func() bool { return sp.procCount() == 1 && p.Active() == 0 }, 3*time.Second)

	if n := sp.procCount(); n != 1 {
		t.Fatalf("expected exactly 1 spawn from the OOM notice, got %d", n)
	}
	stdin := sp.procAt(0).recordedStdin()
	if !contains(stdin, "too much memory") {
		t.Fatalf("spawned prompt missing the OOM notice: %q", stdin)
	}
	if !contains(stdin, "over its 1024 MB limit") {
		t.Fatalf("OOM notice missing the measured detail: %q", stdin)
	}
}

// exitReasonString is the pool's copy of the provider's reason names —
// the labels differ on purpose ("idle" vs "idle_ttl" are persisted in
// spawn logs), but coverage must not: a reason the provider can name and
// the pool logs as "unknown" is how ExitOOM shipped mislabeled.
func TestExitReasonStringCoversEveryReason(t *testing.T) {
	for r := provider.ExitReason(0); r < 32; r++ {
		if provider.ExitReasonName(r) == "unknown" {
			continue
		}
		if got := exitReasonString(r); got == "unknown" {
			t.Errorf("exitReasonString(%d) = \"unknown\", but the provider names it %q",
				r, provider.ExitReasonName(r))
		}
	}
}

// Non-recover system turns (channel origin context, reap notices) must
// keep the buffered-no-spawn behavior even after the recover exception.
func TestReapSystemTurnStillDoesNotSpawn(t *testing.T) {
	sp := &scriptedSpawner{}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	if err := p.Send(context.Background(), "S1", "default", "reap", "system", "[system] connectors reaped"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := sp.procCount(); n != 0 {
		t.Fatalf("reap system turn spawned the agent (%d procs); it must stay buffered", n)
	}
}

// exitReasonString must know every reason exitReasonName knows — ExitOOM
// was added there but not here, so every memory kill logged as "unknown"
// in Recent Spawns while the notice told the operator to go read it.
func TestExitReasonStringOOM(t *testing.T) {
	if got := exitReasonString(provider.ExitOOM); got != "oom" {
		t.Fatalf("exitReasonString(ExitOOM) = %q, want %q", got, "oom")
	}
}
