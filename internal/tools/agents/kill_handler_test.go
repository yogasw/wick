package agents

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/pkg/memreport"
)

func procs() []memreport.Proc {
	return []memreport.Proc{
		{PID: 1, Name: "init"},
		{PID: 42, Name: "wick"},
		{PID: 100, Name: "chrome"},
		{PID: 101, Name: "chrome"},
		{PID: 200, Name: "node"},
	}
}

// Killing wick would take the dashboard down with it, leaving the
// operator with no way to see what just happened.
func TestSelectKillTargets_NeverKillsWick(t *testing.T) {
	targets, skipped := selectKillTargets(procs(), killRequest{PID: 42}, 42)

	if len(targets) != 0 {
		t.Fatalf("targeted %v — wick was not protected", targets)
	}
	if !strings.Contains(strings.Join(skipped, " "), "wick") {
		t.Fatalf("skipped = %v, want an explanation naming wick", skipped)
	}
}

// The refusal is the only thing the operator sees when a row will not
// die, so it has to name the pid and say why. "skipped wick itself" left
// a real operator reading the access log for an explanation that was
// never written down.
func TestSelectKillTargets_RefusalNamesThePIDAndReason(t *testing.T) {
	_, skipped := selectKillTargets(procs(), killRequest{PID: 42}, 42)

	msg := strings.Join(skipped, " ")
	if !strings.Contains(msg, "42") {
		t.Errorf("refusal %q does not name the pid it refused", msg)
	}
	if !strings.Contains(msg, "this page") {
		t.Errorf("refusal %q does not say what ending it would cost", msg)
	}
}

// PID 1 is init: ending it takes down the machine or the container.
func TestSelectKillTargets_NeverKillsInit(t *testing.T) {
	targets, skipped := selectKillTargets(procs(), killRequest{PID: 1}, 42)

	if len(targets) != 0 {
		t.Fatalf("targeted %v — pid 1 was not protected", targets)
	}
	if !strings.Contains(strings.Join(skipped, " "), "pid 1") {
		t.Fatalf("skipped = %v, want an explanation naming pid 1", skipped)
	}
}

// A group kill takes every process sharing the name — that is the point
// of offering it for a browser.
func TestSelectKillTargets_GroupTakesEveryMatch(t *testing.T) {
	targets, _ := selectKillTargets(procs(), killRequest{Name: "chrome"}, 42)

	if len(targets) != 2 {
		t.Fatalf("targeted %v, want both chrome processes", targets)
	}
}

// A group kill must not sweep up wick just because it shares a name with
// something — the protection is per process, not per request shape.
func TestSelectKillTargets_GroupStillProtectsWick(t *testing.T) {
	list := []memreport.Proc{
		{PID: 42, Name: "wick"},
		{PID: 43, Name: "wick"},
	}
	targets, _ := selectKillTargets(list, killRequest{Name: "wick"}, 42)

	for _, pid := range targets {
		if pid == 42 {
			t.Fatal("a group kill included wick itself")
		}
	}
	if len(targets) != 1 || targets[0] != 43 {
		t.Fatalf("targets = %v, want only the other process", targets)
	}
}

// "End 40 things at once" is a bigger action than a single click
// communicates, so a group kill stops at a cap and says so.
func TestSelectKillTargets_CapsAGroupKill(t *testing.T) {
	var list []memreport.Proc
	for i := 0; i < maxGroupKill+10; i++ {
		list = append(list, memreport.Proc{PID: 1000 + i, Name: "chrome"})
	}

	targets, skipped := selectKillTargets(list, killRequest{Name: "chrome"}, 42)

	if len(targets) != maxGroupKill {
		t.Fatalf("targeted %d, want the cap of %d", len(targets), maxGroupKill)
	}
	if len(skipped) == 0 {
		t.Fatal("hit the cap without telling the operator anything was left")
	}
}

// A zombie has already exited. The kernel discards signals sent to it,
// so a kill would report success and change nothing — which is exactly
// how a process appears to survive being killed, over and over.
func TestSelectKillTargets_RefusesAZombie(t *testing.T) {
	list := []memreport.Proc{{PID: 4334, Name: "gotty", Kind: memreport.KindZombie}}

	targets, skipped := selectKillTargets(list, killRequest{PID: 4334}, 42)

	if len(targets) != 0 {
		t.Fatalf("targeted %v — a signal to a zombie does nothing", targets)
	}
	msg := strings.Join(skipped, " ")
	if !strings.Contains(msg, "already exited") {
		t.Errorf("refusal %q does not say the process is already gone", msg)
	}
	if !strings.Contains(msg, "parent") {
		t.Errorf("refusal %q does not say what would actually clear it", msg)
	}
}

// A kernel thread has no user address space and does not take signals
// the way a process does.
func TestSelectKillTargets_RefusesAKernelThread(t *testing.T) {
	list := []memreport.Proc{{PID: 16, Name: "ksoftirqd/0", Kind: memreport.KindKernel}}

	targets, skipped := selectKillTargets(list, killRequest{PID: 16}, 42)

	if len(targets) != 0 {
		t.Fatalf("targeted %v — kernel threads cannot be ended", targets)
	}
	if !strings.Contains(strings.Join(skipped, " "), "kernel thread") {
		t.Fatalf("skipped = %v, want an explanation naming the kernel thread", skipped)
	}
}

// A group kill must still end the live members: one zombie among them is
// not a reason to leave the rest running.
func TestSelectKillTargets_GroupSkipsZombiesButKillsTheRest(t *testing.T) {
	list := []memreport.Proc{
		{PID: 100, Name: "gotty", Kind: memreport.KindZombie},
		{PID: 101, Name: "gotty"},
	}

	targets, skipped := selectKillTargets(list, killRequest{Name: "gotty"}, 42)

	if len(targets) != 1 || targets[0] != 101 {
		t.Fatalf("targets = %v, want only the live process", targets)
	}
	if len(skipped) == 0 {
		t.Fatal("dropped the zombie without saying so")
	}
}

// An empty request must not become "kill everything".
func TestSelectKillTargets_EmptyRequestTargetsNothing(t *testing.T) {
	targets, _ := selectKillTargets(procs(), killRequest{}, 42)

	if len(targets) != 0 {
		t.Fatalf("an empty request targeted %v", targets)
	}
}

// A pid that is gone by the time the request lands is not an error — the
// table refreshes every 10s, so this races constantly.
func TestSelectKillTargets_MissingPIDIsNoTarget(t *testing.T) {
	targets, _ := selectKillTargets(procs(), killRequest{PID: 9999}, 42)

	if len(targets) != 0 {
		t.Fatalf("a vanished pid targeted %v", targets)
	}
}
