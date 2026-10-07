package provider

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// A helper still registered past deadline + grace is killed (its group)
// and dropped by the reaper; one released in time is not there to reap.
func TestReaperKillsPastDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process groups")
	}
	cmd, release := HelperCommand(context.Background(), &Instance{Type: TypeOMP, Name: "reap"}, "omp-usage", "/bin/sleep", "300")
	defer release()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if hs := Helpers(); len(hs) == 0 || hs[len(hs)-1].Label != "omp-usage" || hs[len(hs)-1].Instance != "omp/reap" {
		t.Fatalf("not recorded: %+v", hs)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Not due yet: nothing reaped.
	if n := ReapHelpers(); n != 0 {
		t.Fatalf("reaped %d before the deadline", n)
	}
	prev := helperNow
	helperNow = func() time.Time { return time.Now().Add(helperMaxTimeout + reaperGrace + time.Minute) }
	t.Cleanup(func() { helperNow = prev })
	if n := ReapHelpers(); n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reaped helper still running")
	}
	for _, h := range Helpers() {
		if h.Label == "omp-usage" && h.Instance == "omp/reap" {
			t.Fatal("reaped entry still registered")
		}
	}
}

// release unregisters: a finished helper is not left for the reaper.
func TestReleaseUnregisters(t *testing.T) {
	_, release := HelperCommand(context.Background(), nil, "opencode-models", "/bin/true")
	before := len(Helpers())
	release()
	release() // idempotent
	if len(Helpers()) != before-1 {
		t.Fatalf("release left the entry: %d → %d", before, len(Helpers()))
	}
}

// Boot cleanup stops helper scopes of ANOTHER wick pid only: not this
// process's, and never an agent scope.
func TestCleanupStaleHelperScopes(t *testing.T) {
	withBackend(t, memscope.BackendSystemd)
	self := strconv.Itoa(os.Getpid())
	units := []string{
		"omp-models-agent-4242-7.scope",         // previous wick, helper → stop
		"opencode-catalog-agent-4242-8.scope",   // previous wick, helper → stop
		"omp-models-agent-" + self + "-9.scope", // this process → keep
		"omp-rpc-agent-4242-3.scope",            // agent scope → keep
		"claude-agent-4242-1.scope",             // agent scope → keep
		"unrelated.scope",
	}
	pl, ps, pa := listUserScopes, stopUserScope, ownerPidAlive
	t.Cleanup(func() { listUserScopes, stopUserScope, ownerPidAlive = pl, ps, pa })
	ownerPidAlive = func(int) bool { return false }
	listUserScopes = func(context.Context) ([]string, error) { return units, nil }
	var stopped []string
	stopUserScope = func(_ context.Context, u string) error { stopped = append(stopped, u); return nil }
	if n := CleanupStaleHelperScopes(context.Background()); n != 2 {
		t.Fatalf("stopped %d (%v), want 2", n, stopped)
	}
	if stopped[0] != units[0] || stopped[1] != units[1] {
		t.Fatalf("stopped %v", stopped)
	}
	// A predecessor still draining after a reload keeps its helpers.
	ownerPidAlive = func(pid int) bool { return pid == 4242 }
	stopped = nil
	if n := CleanupStaleHelperScopes(context.Background()); n != 0 {
		t.Fatalf("live predecessor: stopped %v", stopped)
	}
	ownerPidAlive = func(int) bool { return false }
	// Without systemd there is nothing to list.
	withBackend(t, memscope.BackendNone)
	stopped = nil
	if n := CleanupStaleHelperScopes(context.Background()); n != 0 || len(stopped) != 0 {
		t.Fatalf("no systemd: stopped %v", stopped)
	}
}
