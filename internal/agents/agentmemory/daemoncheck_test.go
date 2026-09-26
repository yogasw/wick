package agentmemory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* "Has it spawned, or not?" — the four answers, and especially the two that
   did not exist before: two daemons at once, and wick cannot tell. */

func newCheckBackend(t *testing.T, bin string, procs map[int][]string) *Backend {
	t.Helper()
	exe := map[int]string{}
	for pid := range procs {
		exe[pid] = bin
	}
	fakeProc(t, procs, exe)
	withResolvedBin(t, bin)
	return &Backend{
		Desc: Descriptor{ID: "mem", DisplayName: "ai-memory", BinName: "ai-memory", PrefPort: 49374, HealthPath: "/healthz", Adopt: bindMatch},
		Mgr:  newManager(Descriptor{ID: "mem", DisplayName: "ai-memory", BinName: "ai-memory", PrefPort: 49374, HealthPath: "/healthz", Adopt: bindMatch}),
	}
}

func touchBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ai-memory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Nothing running, and agents have been spawning anyway. This is the state the
// user was actually in, and the one that used to read as a bare "Stopped".
func TestDaemonCheckSaysWhenAgentsAreSpawningWithoutMemory(t *testing.T) {
	bin := touchBin(t)
	be := newCheckBackend(t, bin, nil)

	before := spawnsWithoutMemory.Load()
	noteSpawnWithoutMemory()
	t.Cleanup(func() { spawnsWithoutMemory.Store(before) })

	c := RunDaemonCheck(be)
	if c.Running || c.Port != 0 {
		t.Fatalf("nothing is running: %+v", c)
	}
	if !c.SpawnsWithoutMemory {
		t.Fatal("agents spawned with no memory and the check did not say so")
	}
	if !strings.Contains(c.Verdict, "NO memory") || !strings.Contains(c.Verdict, "cannot tell") {
		t.Fatalf("the verdict has to name the consequence: %q", c.Verdict)
	}
}

// Running, adopted, on a port that is not the preference: all three said out
// loud, because each one changes what wick can promise.
// The port here is deliberately one nothing holds. probeHealth dials for
// real, so using this host's actual daemon port would make the test depend on
// whether that daemon happens to be up.
func TestDaemonCheckDescribesAnAdoptedDaemon(t *testing.T) {
	bin := touchBin(t)
	be := newCheckBackend(t, bin, map[int][]string{
		2223661: {bin, "serve", "--transport", "http", "--bind", "127.0.0.1:49321", "--enable-web"},
	})

	c := RunDaemonCheck(be)
	if !c.Running || c.Managed {
		t.Fatalf("running, and not ours: %+v", c)
	}
	if c.Port != 49321 || c.PrefPort != 49374 {
		t.Fatalf("the real port beside the preferred one: %+v", c)
	}
	// Nothing answers in a test, so it is running-but-silent — which is its
	// own sentence, not the same as stopped.
	if c.Answering {
		t.Fatal("nothing is actually listening in this test")
	}
	if !strings.Contains(c.Verdict, "wedged") {
		t.Fatalf("a live but silent daemon is not 'stopped': %q", c.Verdict)
	}
}

// The state this host was in this morning: two daemons, two stores.
func TestDaemonCheckReportsTwoDaemonsRatherThanPickingOne(t *testing.T) {
	bin := touchBin(t)
	be := newCheckBackend(t, bin, map[int][]string{
		100: {bin, "serve", "--bind", "127.0.0.1:49374"},
		200: {bin, "serve", "--bind", "127.0.0.1:49375"},
	})

	c := RunDaemonCheck(be)
	if len(c.Processes) != 2 {
		t.Fatalf("both are reported: %+v", c.Processes)
	}
	if !strings.Contains(c.Verdict, "2 ai-memory daemons") {
		t.Fatalf("the count leads the sentence: %q", c.Verdict)
	}
	// The consequence, not just the count.
	if !strings.Contains(c.Verdict, "not the memory being written") {
		t.Fatalf("the verdict must say what two stores costs: %q", c.Verdict)
	}
	// And no port is claimed, because there is no single answer.
	if c.Port != 0 {
		t.Fatalf("an ambiguous port must not be resolved to one: %d", c.Port)
	}
}

func TestDaemonCheckPlainlyStopped(t *testing.T) {
	bin := touchBin(t)
	be := newCheckBackend(t, bin, nil)
	before := spawnsWithoutMemory.Load()
	spawnsWithoutMemory.Store(0)
	t.Cleanup(func() { spawnsWithoutMemory.Store(before) })

	c := RunDaemonCheck(be)
	if c.Running || c.SpawnsWithoutMemory {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(c.Verdict, "not running") {
		t.Fatalf("verdict: %q", c.Verdict)
	}
}
