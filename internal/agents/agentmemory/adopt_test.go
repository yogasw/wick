package agentmemory

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

/* The port of a daemon wick did not start.

   wick only records a bound port for daemons it spawned. After `reload
   --binary` the successor inherits a live daemon and has none, and everything
   that needed a URL fell back to the PREFERRED port — which on this host
   pointed at a DIFFERENT ai-memory with a different store, so agents recalled
   and captured somewhere wick does not manage. These pin that the port now
   comes from the process itself, and that an unknown port stays unknown. */

// fakeProc builds a /proc-shaped tree: one directory per pid with an `exe`
// symlink and a NUL-separated `cmdline`, which is the pair adoptedPort reads.
func fakeProc(t *testing.T, procs map[int][]string, exe map[int]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, argv := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var blob []byte
		for _, a := range argv {
			blob = append(blob, []byte(a)...)
			blob = append(blob, 0)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), blob, 0o600); err != nil {
			t.Fatal(err)
		}
		if target, ok := exe[pid]; ok {
			if err := os.Symlink(target, filepath.Join(dir, "exe")); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A non-numeric entry and a pid with no readable exe both have to be
	// skipped rather than crash the walk — /proc is full of both.
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = prev })
	return root
}

// withResolvedBin makes BinPath() answer with a path of the test's choosing,
// so the scan can be pointed at a binary that exists without a real install.
func withResolvedBin(t *testing.T, bin string) {
	t.Helper()
	prev := resolveOnPath
	resolveOnPath = func(string) (string, error) { return bin, nil }
	t.Cleanup(func() { resolveOnPath = prev })
}

// bindMatch is a stand-in for a backend's Descriptor.Adopt: `serve … --bind
// host:port`, matched on the store like the real one.
func bindMatch(argv []string, opt LaunchOptions) (int, bool) {
	var serve bool
	var dir, bind string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "serve":
			serve = true
		case "--data-dir":
			if i+1 < len(argv) {
				dir, i = argv[i+1], i+1
			}
		case "--bind":
			if i+1 < len(argv) {
				bind, i = argv[i+1], i+1
			}
		}
	}
	if !serve || bind == "" || dir != opt.DataDir {
		return 0, false
	}
	p, err := strconv.Atoi(bind[len("127.0.0.1:"):])
	if err != nil {
		return 0, false
	}
	return p, true
}

func TestAdoptedPortReadsTheProcessesOwnBind(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ai-memory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The line this host's daemon actually carries.
	fakeProc(t,
		map[int][]string{
			4242: {bin, "serve", "--transport", "http", "--bind", "127.0.0.1:49375", "--enable-web"},
			99:   {"/usr/bin/bash", "-c", "something else"},
		},
		map[int]string{4242: bin, 99: "/usr/bin/bash"},
	)

	port, ok := adoptedPort(bin, LaunchOptions{}, bindMatch)
	if !ok || port != 49375 {
		t.Fatalf("the bound port comes from the process, got %d ok=%v", port, ok)
	}
}

// The failure this whole change exists for: another daemon of the same kind,
// on the PREFERRED port, pointed at a different store. Picking it is how
// agents ended up reading and writing somebody else's memory.
func TestAdoptedPortIgnoresADaemonOnAnotherStore(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ai-memory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeProc(t,
		map[int][]string{
			1: {bin, "--data-dir", "/somebody/elses/data", "serve", "--bind", "127.0.0.1:49374"},
			2: {bin, "--data-dir", "/wick/store", "serve", "--bind", "127.0.0.1:49375"},
		},
		map[int]string{1: bin, 2: bin},
	)

	port, ok := adoptedPort(bin, LaunchOptions{DataDir: "/wick/store"}, bindMatch)
	if !ok || port != 49375 {
		t.Fatalf("the daemon on wick's own store is the one that counts, got %d ok=%v", port, ok)
	}
}

func TestAdoptedPortFindsNothingToReport(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ai-memory")
	fakeProc(t, map[int][]string{7: {"/usr/bin/bash"}}, map[int]string{7: "/usr/bin/bash"})

	if _, ok := adoptedPort(bin, LaunchOptions{}, bindMatch); ok {
		t.Fatal("no process of ours is running, so there is no port to report")
	}
	// A backend that cannot read its own launch line reports nothing rather
	// than letting the caller fall back to a preference.
	if _, ok := adoptedPort(bin, LaunchOptions{}, nil); ok {
		t.Fatal("no matcher means no answer")
	}
	if _, ok := adoptedPort("", LaunchOptions{}, bindMatch); ok {
		t.Fatal("no binary means no answer")
	}
}

func TestProcArgvDropsTheTrailingNUL(t *testing.T) {
	fakeProc(t, map[int][]string{5: {"a", "b", "c"}}, nil)
	if got := procArgv(5); len(got) != 3 || got[2] != "c" {
		t.Fatalf("argv: %q", got)
	}
	if got := procArgv(404); got != nil {
		t.Fatalf("a pid that is gone has no argv: %q", got)
	}
}

/* The manager's side of it. */

func TestBoundPortPrefersTheAdoptedPortOverThePreference(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ai-memory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeProc(t,
		map[int][]string{11: {bin, "serve", "--bind", "127.0.0.1:49375"}},
		map[int]string{11: bin},
	)

	withResolvedBin(t, bin)
	m := newManager(Descriptor{ID: "mem", PrefPort: 49374, BinName: "ai-memory", Adopt: bindMatch})

	if got := m.PrefPort(); got != 49374 {
		t.Fatalf("the preference is still the preference: %d", got)
	}
	if got := m.BoundPort(); got != 49375 {
		t.Fatalf("an adopted daemon's port comes from the daemon, got %d", got)
	}
	if got := m.BaseURL(); got != "http://127.0.0.1:49375" {
		t.Fatalf("the URL agents get must be the real one, got %q", got)
	}
}

// The no-guess rule. A preference is where the NEXT start will try; it is not
// evidence about a daemon that is already running, and handing it out as one
// is what wired agents to the wrong store.
func TestUnknownPortIsUnknownRatherThanThePreference(t *testing.T) {
	fakeProc(t, map[int][]string{}, nil)
	bin := filepath.Join(t.TempDir(), "ai-memory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	withResolvedBin(t, bin)
	m := newManager(Descriptor{ID: "mem", PrefPort: 49374, BinName: "ai-memory", Adopt: bindMatch})

	if got := m.BoundPort(); got != 0 {
		t.Fatalf("no evidence means no port, got %d", got)
	}
	if got := m.BaseURL(); got != "" {
		t.Fatalf("no port means no URL, got %q", got)
	}
	// And nothing is reported as running off the back of a guess.
	if m.probeHealth() {
		t.Fatal("an unknown port must not be probed, let alone reported healthy")
	}
}

/* The controls, on a daemon wick did not start.

   Before this, "has it spawned?" was answered from wick's own memory of what
   it started — which a handover erases. The panel then said Stopped beside a
   daemon that was answering, and Start would have bound a SECOND one on the
   preferred port: two daemons, two stores, which is the state that put agents
   on memory nobody was writing to. */

func TestStartRefusesToAddASecondDaemon(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ai-memory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeProc(t,
		map[int][]string{2223661: {bin, "serve", "--bind", "127.0.0.1:49375"}},
		map[int]string{2223661: bin},
	)
	withResolvedBin(t, bin)

	m := newManager(Descriptor{
		ID: "mem", DisplayName: "ai-memory", BinName: "ai-memory", PrefPort: 49374,
		Adopt: bindMatch,
		Launch: func(LaunchOptions) ([]string, []string) {
			return []string{"serve"}, nil
		},
	})

	err := m.start()
	if err == nil {
		t.Fatal("starting beside a running daemon is how this host ended up with two")
	}
	// The refusal has to be actionable: which process, on which port.
	if !strings.Contains(err.Error(), "2223661") || !strings.Contains(err.Error(), "49375") {
		t.Fatalf("the refusal must name what is already running: %v", err)
	}
	if m.spawnedHere() {
		t.Fatal("nothing should have been spawned")
	}
}

// Several daemons is a real state and gets reported as one. Picking the first
// would hide the two-store split that caused all of this.
func TestDaemonsListsEveryMatchRatherThanChoosing(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ai-memory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeProc(t,
		map[int][]string{
			200: {bin, "serve", "--bind", "127.0.0.1:49375"},
			100: {bin, "serve", "--bind", "127.0.0.1:49374"},
		},
		map[int]string{100: bin, 200: bin},
	)
	withResolvedBin(t, bin)
	m := newManager(Descriptor{ID: "mem", BinName: "ai-memory", PrefPort: 49374, Adopt: bindMatch})

	got := m.Daemons()
	if len(got) != 2 || got[0].PID != 100 || got[1].PID != 200 {
		t.Fatalf("both daemons, in pid order: %+v", got)
	}
	// And with two of them, there is no single answer to "which port" — so
	// none is given, rather than one being picked at random.
	if p := m.BoundPort(); p != 0 {
		t.Fatalf("two daemons means the port is ambiguous, got %d", p)
	}
	if got := describeDaemons(got); !strings.Contains(got, "pid 100 on port 49374") {
		t.Fatalf("the description names each one: %q", got)
	}
}

// Matching is on the RESOLVED executable. A stranger's unrelated build that
// happens to be called ai-memory must never be adopted, and above all must
// never be signalled.
func TestDaemonsIgnoresAnUnrelatedBinaryOfTheSameName(t *testing.T) {
	dir := t.TempDir()
	ours := filepath.Join(dir, "ai-memory")
	theirs := filepath.Join(dir, "somebody-else", "ai-memory")
	if err := os.WriteFile(ours, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(theirs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeProc(t,
		map[int][]string{9: {theirs, "serve", "--bind", "127.0.0.1:49374"}},
		map[int]string{9: theirs},
	)
	withResolvedBin(t, ours)
	m := newManager(Descriptor{ID: "mem", BinName: "ai-memory", PrefPort: 49374, Adopt: bindMatch})

	if got := m.Daemons(); len(got) != 0 {
		t.Fatalf("another program with the same name is not ours to touch: %+v", got)
	}
}
