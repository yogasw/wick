package agentmemory

import (
	"os"
	"path/filepath"
	"sort"
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

// DaemonProcess is one running process of a backend, as the process list shows
// it: which pid, and which port its own launch line says it bound.
//
// It is the answer to "has this backend spawned, and where" — asked of the
// machine rather than of wick's memory of what it started (Yoga, 2026-09-26:
// "kalau emang udah start pengen di kill ya cari pid nya aja dari list proses
// gitu").
type DaemonProcess struct {
	PID  int `json:"pid"`
	Port int `json:"port"`
}

// findDaemons lists every process running THIS backend's binary whose launch
// line the backend recognises as its own daemon.
//
// It returns all of them, in pid order, and deliberately does not choose. Two
// daemons of the same backend is not hypothetical — it is what happened on
// this host, and it is the state that made agents read one store while the
// panel described another. A function that silently returned the first would
// hide exactly the thing worth showing.
//
// The match is on the RESOLVED executable, never on the process name: killing
// or adopting a stranger's binary that happens to be called "ai-memory" is a
// different and much worse bug.
func findDaemons(bin string, opt LaunchOptions, match func([]string, LaunchOptions) (int, bool)) []DaemonProcess {
	bin = strings.TrimSpace(bin)
	if bin == "" || match == nil {
		return nil
	}
	// The binary may be reached through a symlink; compare what both sides
	// resolve to, or a link-farm install never matches its own process.
	want, err := filepath.EvalSymlinks(bin)
	if err != nil {
		want = bin
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var out []DaemonProcess
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		exe, err := os.Readlink(filepath.Join(procRoot, e.Name(), "exe"))
		if err != nil {
			// Another user's process, or one that exited between the
			// listing and here. Neither is ours to report.
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
			out = append(out, DaemonProcess{PID: pid, Port: port})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// adoptedPort is the port of a running daemon, for the common case of exactly
// one. Zero when there is none — and also when there are SEVERAL, because
// picking one of two daemons on two stores is the bug this exists to prevent;
// the caller surfaces that state instead (Manager.Daemons).
func adoptedPort(bin string, opt LaunchOptions, match func([]string, LaunchOptions) (int, bool)) (int, bool) {
	found := findDaemons(bin, opt, match)
	if len(found) != 1 {
		return 0, false
	}
	return found[0].Port, true
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
