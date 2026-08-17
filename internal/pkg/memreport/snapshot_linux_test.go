//go:build linux || android

package memreport

import "testing"

// A zero RSS has three unrelated causes, and a table that renders them
// identically sends the operator looking for a measurement bug that is
// not there. These bodies are trimmed from real /proc/<pid>/status.
func TestParseStatusReadsStateAndRSS(t *testing.T) {
	// A zombie: exited, parent has not reaped it. Its address space is
	// already torn down, so there is no VmRSS line.
	zombie := "Name:\tgotty\nState:\tZ (zombie)\nTgid:\t4334\nPid:\t4334\nPPid:\t1\nThreads:\t1\n"
	// A kernel thread also has no VmRSS line — but status alone cannot
	// prove it is one, so it reads as normal here and Snapshot decides.
	kthread := "Name:\tksoftirqd/0\nState:\tS (sleeping)\nTgid:\t16\nPid:\t16\nPPid:\t2\nThreads:\t1\n"
	normal := "Name:\twick\nState:\tS (sleeping)\nTgid:\t900\nPid:\t900\nPPid:\t1\nVmRSS:\t   51200 kB\n"

	cases := []struct {
		name     string
		pid      int
		body     string
		wantKind ProcKind
		wantRSS  uint64
	}{
		{"zombie", 4334, zombie, KindZombie, 0},
		{"kernel thread is not decided here", 16, kthread, KindNormal, 0},
		{"ordinary process", 900, normal, KindNormal, 51200 * 1024},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseStatus(c.pid, c.body)
			if got.Kind != c.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, c.wantKind)
			}
			if got.RSSBytes != c.wantRSS {
				t.Errorf("RSSBytes = %d, want %d", got.RSSBytes, c.wantRSS)
			}
		})
	}
}

// Classification runs against the live /proc rather than a fixture,
// because a fixture only ever confirms what its author already believed.
//
// The first version of this used "child of pid 2" to spot a kernel
// thread. That is true on a conventional boot and false under WSL, where
// pid 2 is init-systemd — an ordinary process whose children were then
// reported as kernel threads holding megabytes of RSS. A fixture would
// never have caught it; this did.
func TestSnapshotClassifiesAgainstRealProc(t *testing.T) {
	procs, err := Snapshot()
	if err != nil {
		t.Skipf("no readable /proc: %v", err)
	}
	if len(procs) == 0 {
		t.Skip("/proc listed no processes")
	}

	var normals int
	for _, p := range procs {
		switch p.Kind {
		case KindKernel:
			// The defining property: no user address space, so nothing to
			// report. A kernel thread with RSS means the rule is wrong.
			if p.RSSBytes != 0 {
				t.Errorf("%s (pid %d) is labelled a kernel thread but holds %d bytes",
					p.Name, p.PID, p.RSSBytes)
			}
			if p.Cmdline != "" {
				t.Errorf("%s (pid %d) is labelled a kernel thread but has a command line %q",
					p.Name, p.PID, p.Cmdline)
			}
		case KindNormal:
			normals++
		}
	}
	if normals == 0 {
		t.Error("no ordinary processes found — everything was misclassified")
	}
}

// /proc/<pid>/cmdline separates argv with NUL bytes and usually ends with
// one. Converting it naively yields a line with embedded NULs that renders
// as a single run-together word — the opposite of the readability this
// field exists for.
func TestParseCmdline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"argv joined with spaces", "node\x00server.js\x00--port\x008080\x00", "node server.js --port 8080"},
		{"single argument", "/usr/bin/gopls\x00", "/usr/bin/gopls"},
		{"no trailing NUL", "grep\x00-r\x00foo", "grep -r foo"},
		// Kernel threads have an empty cmdline; the caller falls back to
		// the process name rather than rendering a blank row.
		{"kernel thread", "", ""},
		{"only NULs", "\x00\x00", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseCmdline(c.in); got != c.want {
				t.Fatalf("parseCmdline(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
