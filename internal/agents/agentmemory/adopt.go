package agentmemory

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Finding the port of a daemon wick did NOT start (Yoga, 2026-09-26).
//
// wick records a bound port when it spawns the daemon itself. It does not
// survive a handover: `reload --binary` hands the listener to a successor
// process that inherits a RUNNING daemon it never spawned, so its recorded
// port is zero and everything that needed a URL fell back to the PREFERRED
// port instead.
//
// On a host where the daemon holds its preference those two numbers are equal,
// which is why this hid. They stopped being equal here: wick's daemon bound
// 49375 because something else already held 49374, and every agent spawned
// after the handover was handed http://127.0.0.1:49374/mcp — first a
// different daemon with a different store (recall and capture both landing
// somewhere wick does not manage), then, once that one was killed, nothing at
// all. "aku udah import terus jalanin codex, tapi pas hai masih ngak ada
// memory nya" is exactly that.
//
// The honest source is the daemon's own command line. It carries the bind
// address wick (or whoever) gave it — `serve --transport http --bind
// 127.0.0.1:49375 --enable-web` on this host — so the port is read from the
// process rather than assumed from a preference. Matching is on the resolved
// binary AND the store, so a second daemon of the same kind pointed at another
// data dir is not mistaken for ours: that mistake is the bug, not the fix.
//
// Linux only, like processRSS next door in resources.go. Elsewhere this finds
// nothing, and "nothing" is reported as unknown rather than papered over with
// the preference — see BoundPort.

// procRoot is /proc, indirected so the scan can be pointed at a fixture.
var procRoot = "/proc"

// adoptedPort scans for a running process of this backend and returns the
// loopback port its own command line says it bound.
//
// bin is the resolved binary wick would start; opt is what wick would start it
// with. match is the backend's own reader of its launch line (Descriptor.Adopt)
// — the flag vocabulary belongs to the backend, not here.
func adoptedPort(bin string, opt LaunchOptions, match func([]string, LaunchOptions) (int, bool)) (int, bool) {
	bin = strings.TrimSpace(bin)
	if bin == "" || match == nil {
		return 0, false
	}
	// The binary may be reached through a symlink; compare what both sides
	// resolve to, or a link-farm install never matches its own process.
	want, err := filepath.EvalSymlinks(bin)
	if err != nil {
		want = bin
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		exe, err := os.Readlink(filepath.Join(procRoot, e.Name(), "exe"))
		if err != nil {
			// Not ours to read, or gone between the listing and here.
			continue
		}
		if exe != want && exe != bin {
			continue
		}
		argv := procArgv(pid)
		if len(argv) == 0 {
			continue
		}
		// argv[0] is the program; the launch line starts after it.
		if port, ok := match(argv[1:], opt); ok && port > 0 {
			return port, true
		}
	}
	return 0, false
}

// procArgv reads a process's argument vector. The file is NUL-separated with a
// trailing NUL, which would otherwise produce a phantom empty final argument.
func procArgv(pid int) []string {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
