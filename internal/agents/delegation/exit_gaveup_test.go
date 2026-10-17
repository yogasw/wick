package delegation

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The Done of an exit whose retries are spent is an exit that is not
// restarting, and carries the plain reason.
func TestExitDoneRawGaveUp(t *testing.T) {
	raw := ExitDoneRawGaveUp("stopped after 2 retries: no output for 20m")
	if exited, restarting := ParseExitDone(raw); !exited || restarting {
		t.Fatalf("ParseExitDone = %v, %v", exited, restarting)
	}
	if got := ExitDoneReason(raw); got != "stopped after 2 retries: no output for 20m" {
		t.Fatalf("reason = %q", got)
	}
	if got := ExitDoneReason(ExitDoneRaw(true)); got != "" {
		t.Fatalf("restart Done has a reason: %q", got)
	}
}

type waitRunner struct {
	Runner
	note string
}

func (r waitRunner) WaitNote(string) string { return r.note }

// A pending sub-agent being restarted reads as still working, with the
// wait named, never as failed.
func TestCollectNotesWait(t *testing.T) {
	s := &Service{Runner: waitRunner{note: "target restarting after an idle timeout, retry 1/2"}}
	out := &CollectResult{Status: entity.DelegationRunning, Pending: true, Note: "Still working — this is a progress check."}
	s.noteWait(out, &entity.AgentDelegation{ChildSessionID: "c1"})
	if out.Wait != "target restarting after an idle timeout, retry 1/2" || !strings.HasPrefix(out.Note, "Still working: target restarting") ||
		out.Status != entity.DelegationRunning {
		t.Fatalf("out = %+v", out)
	}
	quiet := &CollectResult{Note: "x"}
	(&Service{Runner: waitRunner{}}).noteWait(quiet, &entity.AgentDelegation{})
	if quiet.Wait != "" || quiet.Note != "x" {
		t.Fatalf("quiet = %+v", quiet)
	}
}
