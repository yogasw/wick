//go:build linux && integration

package memscope

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
)

// Wick tears a session down with kill(-pgid) across the whole process
// group. Routing the spawn through systemd-run inserts a process between
// wick and the agent, so this asserts that relationship survives it.
//
// If it does not, wick keeps its memory guard and loses the ability to
// stop a session at all — a worse bug than the one being fixed. The
// production preflight verifies the same property by hand; this test
// exists so a later refactor cannot break it silently.
func TestKillProcessGroupReapsTreeThroughScope(t *testing.T) {
	if !Available() {
		t.Skip("no systemd user session")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "tree.sh")
	pidFile := filepath.Join(dir, "tree.pid")

	// A parent with both a direct child and a nested grandchild: the
	// shapes an agent actually produces (MCP server, tool subprocess).
	body := "#!/bin/bash\n" +
		"sleep 300 &\n" +
		"( sleep 300 & wait ) &\n" +
		"echo $$ > " + pidFile + "\n" +
		"sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	bin, argv := WrapArgv("/bin/bash", []string{script}, Opts{
		Unit: "wick-teardown-test", Slice: SliceName, LimitMB: 200,
	})
	cmd := safeexec.Command(bin, argv...)
	// Mirrors procgroup.Apply: the agent is the root of its own group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()

	var treePid int
	for i := 0; i < 40; i++ {
		if b, err := os.ReadFile(pidFile); err == nil {
			if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				treePid = p
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if treePid == 0 {
		t.Fatal("test tree never started")
	}

	pgid, err := syscall.Getpgid(treePid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	before := countInGroup(t, pgid)
	if before == 0 {
		t.Fatal("no processes in the group before the kill")
	}
	t.Logf("process group %d holds %d processes before teardown", pgid, before)

	// Exactly what wick does to stop a session.
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill(-pgid): %v", err)
	}
	time.Sleep(1 * time.Second)

	if left := countInGroup(t, pgid); left != 0 {
		t.Fatalf("%d processes survived kill(-pgid) through a scope — session teardown is broken", left)
	}
}

// countInGroup counts live processes in a process group by walking /proc,
// so the test does not depend on pgrep being installed.
func countInGroup(t *testing.T, pgid int) int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	n := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if got, err := syscall.Getpgid(pid); err == nil && got == pgid {
			n++
		}
	}
	return n
}

// The design's central claim is that only the offending agent dies. That
// is worth asserting directly rather than inferring from a per-scope
// limit: a sibling scope must be untouched while one is being killed.
func TestOOMKillIsScopedToOneAgent(t *testing.T) {
	if !Available() {
		t.Skip("no systemd user session")
	}
	dir := t.TempDir()
	hog := filepath.Join(dir, "hog.py")
	// Pages must be touched, or they are never charged to the cgroup.
	body := "buf=[]\n" +
		"while True:\n" +
		"    b=bytearray(5*1024*1024)\n" +
		"    for i in range(0, len(b), 4096): b[i] = 1\n" +
		"    buf.append(b)\n"
	if err := os.WriteFile(hog, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// The innocent sibling: alive in its own scope for the whole test.
	sibBin, sibArgv := WrapArgv("/bin/sleep", []string{"30"}, Opts{
		Unit: "wick-sibling-test", Slice: SliceName, LimitMB: 200,
	})
	sib := safeexec.Command(sibBin, sibArgv...)
	sib.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := sib.Start(); err != nil {
		t.Fatalf("start sibling: %v", err)
	}
	defer func() { _ = syscall.Kill(-sib.Process.Pid, syscall.SIGKILL) }()

	hogBin, hogArgv := WrapArgv("/usr/bin/python3", []string{hog}, Opts{
		Unit: "wick-hog-test", Slice: SliceName, LimitMB: 250,
	})
	out, err := safeexec.Command(hogBin, hogArgv...).CombinedOutput()
	if err == nil {
		t.Fatalf("hog was not killed; output: %s", out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if code := ee.ExitCode(); code != 137 {
			t.Fatalf("hog exit code = %d, want 137 (SIGKILL)", code)
		}
	}

	// The sibling must still be running: the kill reached one scope only.
	if sib.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("sibling agent died alongside the offender — the kill was not scoped")
	}
}
