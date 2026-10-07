package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
)

// withHelperGuard installs g as the helper guard policy.
func withHelperGuard(t *testing.T, g *MemGuard) {
	t.Helper()
	prev := HelperGuard
	HelperGuard = func() *MemGuard { return g }
	t.Cleanup(func() { HelperGuard = prev })
}

// Every helper (models listing, export/import, probes) runs in its own
// labelled scope with the INSTANCE's limit, else the guard's default —
// the agent spawn's rule (config.ResolveAgentLimitMB).
func TestHelperCommandWrapsWithInstanceLimit(t *testing.T) {
	withScopesAvailable(t, true)
	withHelperGuard(t, &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1536})

	ins := Instance{Type: TypeOMP, Name: "yoga", MemoryMaxMB: 700}
	cmd, release := HelperCommand(context.Background(), &ins, HelperLabel(TypeOMP, "models"), "/bin/omp", "--profile", "wick-yoga", "models", "--json")
	defer release()
	argv := strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, "MemoryMax=700M") || !strings.Contains(argv, "omp-models") || !strings.HasSuffix(argv, "/bin/omp --profile wick-yoga models --json") {
		t.Fatalf("instance limit / label not applied: %s", argv)
	}
	// No instance known: the guard's default.
	cmd, release2 := HelperCommand(context.Background(), nil, "opencode-export", "/bin/opencode", "export", "ses_1")
	defer release2()
	if a := strings.Join(cmd.Args, " "); !strings.Contains(a, "MemoryMax=1536M") || !strings.Contains(a, "opencode-export") {
		t.Fatalf("default limit not applied: %s", a)
	}
}

// Measure: a scope (peaks recorded) and no limit. Off / no guard: the
// argv untouched, exactly as before.
func TestHelperCommandModes(t *testing.T) {
	withScopesAvailable(t, true)
	withHelperGuard(t, &MemGuard{Mode: config.MemGuardMeasure, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1536})
	cmd, rel := HelperCommand(context.Background(), &Instance{MemoryMaxMB: 700}, "omp-usage", "/bin/omp", "usage")
	rel()
	if a := strings.Join(cmd.Args, " "); !strings.Contains(a, "omp-usage") || strings.Contains(a, "MemoryMax=700M") || strings.Contains(a, "MemoryMax=1536M") {
		t.Fatalf("measure must wrap without a limit: %s", a)
	}
	for _, g := range []*MemGuard{nil, {Mode: config.MemGuardOff, Scopes: config.GuardScopes{OnSpawn: true}}} {
		withHelperGuard(t, g)
		cmd, rel := HelperCommand(context.Background(), nil, "omp-usage", "/bin/omp", "usage")
		rel()
		if len(cmd.Args) != 2 || cmd.Args[0] != "/bin/omp" {
			t.Fatalf("off/nil guard changed the argv: %v", cmd.Args)
		}
	}
}

// Model listings never run two at a time, whatever the instance: the
// helper slot is global.
func TestHelperSlotSerializesAcrossInstances(t *testing.T) {
	var running, maxRunning atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			free, err := AcquireHelperSlot(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			n := running.Add(1)
			for {
				m := maxRunning.Load()
				if n <= m || maxRunning.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			free()
		}()
	}
	wg.Wait()
	if maxRunning.Load() != 1 {
		t.Fatalf("%d listings ran at once", maxRunning.Load())
	}
	// A cancelled wait does not hang and frees nothing.
	free, _ := AcquireHelperSlot(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AcquireHelperSlot(ctx); err == nil {
		t.Fatal("cancelled wait acquired the slot")
	}
	free()
}

// Two instances' CachedCLIModels through the real runner path: never two
// listings at once; a forced refresh right after a fetch reuses it.
func TestCachedCLIModelsSerializedAndMinRefresh(t *testing.T) {
	var running, maxRunning, calls atomic.Int32
	prev := cliModelsRunner
	cliModelsRunner = func(ctx context.Context, bin string, args, env []string) ([]byte, error) {
		free, err := AcquireHelperSlot(ctx)
		defer free()
		if err != nil {
			return nil, err
		}
		calls.Add(1)
		n := running.Add(1)
		if n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return []byte(`{"models":[{"selector":"openai-codex/gpt-5.6-luna","kind":"chat"}]}`), nil
	}
	t.Cleanup(func() { cliModelsRunner = prev })
	a := Instance{Type: TypeOMP, Name: "slot-a", Binary: "/bin/true", OMPConfig: &OMPConfig{Profile: "wick-a"}}
	b := Instance{Type: TypeOMP, Name: "slot-b", Binary: "/bin/true", OMPConfig: &OMPConfig{Profile: "wick-b"}}
	t.Cleanup(func() {
		cliModelsMu.Lock()
		delete(cliModelsCache, cliModelsKey(a))
		delete(cliModelsCache, cliModelsKey(b))
		cliModelsMu.Unlock()
	})
	var wg sync.WaitGroup
	for _, ins := range []Instance{a, b, a, b} {
		wg.Add(1)
		go func(ins Instance) { defer wg.Done(); _, _, _ = CachedCLIModels(context.Background(), ins, true) }(ins)
	}
	wg.Wait()
	if maxRunning.Load() != 1 {
		t.Fatalf("%d listings ran concurrently", maxRunning.Load())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want one per instance (in-flight reused)", calls.Load())
	}
	// "Fresh fetch on open" right after: served from the fetch just made.
	if _, _, err := CachedCLIModels(context.Background(), a, true); err != nil || calls.Load() != 2 {
		t.Fatalf("refresh inside the minimum interval re-ran the CLI: calls=%d err=%v", calls.Load(), err)
	}
}

// The spawn about to start keeps its own session's server and, for
// omp/opencode, its instance's; everything else idle may go.
func TestYieldIdleServersKeepRule(t *testing.T) {
	type q struct{ inst, group string }
	var kept, dropped []q
	RegisterIdleYielder("test", func(keep func(instance, group string) bool) int {
		n := 0
		for _, c := range []q{{"waba", "waba/s1"}, {"yoga", "yoga/s2"}, {"oc", "oc"}, {"yoga", "yoga/s1"}} {
			if keep(c.inst, c.group) {
				kept = append(kept, c)
			} else {
				dropped = append(dropped, c)
				n++
			}
		}
		return n
	})
	t.Cleanup(func() { RegisterIdleYielder("test", nil) })
	if n := YieldIdleServers("s1", "oc"); n != 1 {
		t.Fatalf("stopped %d, want 1 (yoga/s2)", n)
	}
	if len(dropped) != 1 || dropped[0].group != "yoga/s2" {
		t.Fatalf("dropped %v, kept %v", dropped, kept)
	}
}

// fakeCLI writes an executable named name that prints out.
func fakeCLI(t *testing.T, name, out string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\ncat <<'EOF'\n"+out+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// observeHelpers records every helper label built during the test.
func observeHelpers(t *testing.T) *[]string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	t.Cleanup(ObserveHelpersForTest(func(label string, _ *Instance) {
		mu.Lock()
		got = append(got, label)
		mu.Unlock()
	}))
	return &got
}

// `omp models --json` and the version probe — the two helpers this
// package starts itself — go through HelperCommand.
func TestProviderHelperExecPathsUseGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	got := observeHelpers(t)
	omp := fakeCLI(t, "omp", `{"models":[{"selector":"openai-codex/gpt-5.6-luna","kind":"chat"}]}`)
	ins := Instance{Type: TypeOMP, Name: "hp-omp", Binary: omp, OMPConfig: &OMPConfig{Profile: "wick-hp"}}
	if m, err := ListCLIModels(context.Background(), ins); err != nil || len(m) != 1 {
		t.Fatalf("models: %v %v", m, err)
	}
	_ = Probe(context.Background(), ins)
	joined := strings.Join(*got, ",")
	if !strings.Contains(joined, "omp-models") || !strings.Contains(joined, "omp-version") {
		t.Fatalf("helpers seen: %v", *got)
	}
}

// A changed login drops the cached list even inside the minimum interval.
func TestInvalidateCLIModelsBypassesMinRefresh(t *testing.T) {
	calls := stubCLIModels(t, "openai/gpt-5.5\n", nil)
	ins := liveTestInstance(t)
	CachedCLIModels(context.Background(), ins, true)
	InvalidateCLIModels(ins)
	CachedCLIModels(context.Background(), ins, true)
	if *calls != 2 {
		t.Fatalf("calls = %d, want a fetch after invalidation", *calls)
	}
}

// pidAlive reports whether pid still exists (signal 0).
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			var pid int
			fmt.Sscan(strings.TrimSpace(string(b)), &pid)
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("child pid never written")
	return 0
}

// A helper that hangs past its timeout is killed together with the child
// it forked (bun does fork); one that finished leaves no child behind
// either, because release kills its group.
func TestHelperTimeoutKillsWholeGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process groups")
	}
	dir := t.TempDir()
	childPid := filepath.Join(dir, "child.pid")
	hang := probeScript(t, dir, "omp", "sleep 300 &\necho $! > "+childPid+"\nsleep 300")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd, release := HelperCommand(ctx, &Instance{Type: TypeOMP}, "omp-version", hang)
	start := time.Now()
	_ = cmd.Run()
	release()
	if time.Since(start) > 5*time.Second {
		t.Fatalf("hung helper not killed on timeout (%s)", time.Since(start))
	}
	child := readPid(t, childPid)
	time.Sleep(100 * time.Millisecond)
	if pidAlive(cmd.Process.Pid) || pidAlive(child) {
		t.Fatalf("leftover: leader alive=%v child alive=%v", pidAlive(cmd.Process.Pid), pidAlive(child))
	}

	// Finished helper that left a background child: release reaps it.
	orphanPid := filepath.Join(dir, "orphan.pid")
	quick := probeScript(t, t.TempDir(), "opencode", "sleep 300 >/dev/null 2>&1 &\necho $! > "+orphanPid+"\necho done")
	cmd2, release2 := HelperCommand(context.Background(), nil, "opencode-models", quick)
	if out, err := cmd2.Output(); err != nil || !strings.Contains(string(out), "done") {
		t.Fatalf("quick helper: %q %v", out, err)
	}
	orphan := readPid(t, orphanPid)
	release2()
	time.Sleep(100 * time.Millisecond)
	if pidAlive(orphan) {
		t.Fatal("child of a finished helper survived release")
	}
}

func TestHelperTimeouts(t *testing.T) {
	if d := helperTimeout(&Instance{Type: TypeOMP}, "omp-version"); d != heavyVersionTimeout {
		t.Fatalf("omp version: %s", d)
	}
	if d := helperTimeout(&Instance{Type: TypeClaude}, "claude-version"); d != lightVersionTimeout {
		t.Fatalf("claude version: %s", d)
	}
	if d := helperTimeout(nil, "omp-models"); d != helperMaxTimeout {
		t.Fatalf("models: %s", d)
	}
}
