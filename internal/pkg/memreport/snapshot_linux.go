//go:build linux || android

package memreport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var procRoot = "/proc"

// Snapshot samples every readable process.
//
// Processes that vanish mid-walk are skipped rather than failing the
// sample: a snapshot of a moving system is expected to be slightly
// stale, not impossible.
func Snapshot() ([]Proc, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var out []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a pid directory
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "status"))
		if err != nil {
			continue // exited between ReadDir and here
		}
		p := parseStatus(pid, string(b))
		hasCmdline := false
		// CPU and IO are best-effort: /proc/<pid>/io is unreadable for
		// processes owned by another user, and either file can vanish
		// mid-walk. A missing counter reads as 0 rather than dropping the
		// process — its memory is still worth reporting.
		if st, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "stat")); err == nil {
			p.CPUTicks = parseCPUTicks(string(st))
		}
		if io, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "io")); err == nil {
			p.IOReadBytes, p.IOWriteBytes = parseIO(string(io))
		}
		if cl, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline")); err == nil {
			p.Cmdline = parseCmdline(string(cl))
			hasCmdline = p.Cmdline != ""
		}
		// A kernel thread is identified by having no cmdline at all, which
		// is a property of the thing itself rather than a guess from its
		// ancestry. The obvious rule — "child of pid 2" — is only true on
		// a conventional boot: under WSL pid 2 is init-systemd, an
		// ordinary user process, and that rule mislabels its children as
		// kernel threads with megabytes of RSS.
		//
		// Applied here rather than in parseStatus because status alone
		// cannot tell: the evidence lives in a different file.
		if p.Kind == KindNormal && !hasCmdline {
			p.Kind = KindKernel
		}
		out = append(out, p)
	}
	return out, nil
}

// parseCPUTicks pulls utime+stime (fields 14 and 15) out of
// /proc/<pid>/stat.
//
// Field 2 is the executable name in parentheses and may itself contain
// spaces and parentheses — "(my prog)" — so splitting the whole line on
// whitespace misaligns every later field. Everything is therefore indexed
// from the LAST ')', which is the documented way to parse this file.
func parseCPUTicks(body string) uint64 {
	close := strings.LastIndex(body, ")")
	if close < 0 || close+2 > len(body) {
		return 0
	}
	// After "<pid> (<comm>) " field 3 is state; utime/stime are fields
	// 14/15 overall, i.e. index 11/12 counting from state.
	f := strings.Fields(body[close+1:])
	if len(f) < 13 {
		return 0
	}
	utime, err1 := strconv.ParseUint(f[11], 10, 64)
	stime, err2 := strconv.ParseUint(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0
	}
	return utime + stime
}

// parseCmdline turns /proc/<pid>/cmdline into a readable command.
//
// The file separates argv entries with NUL bytes and usually ends with
// one, so a naive string conversion yields a line with embedded NULs that
// renders as one run-together word. Splitting and rejoining with spaces
// is what makes it match what the operator typed.
//
// Kernel threads have an empty cmdline; the caller falls back to the
// process name for those.
func parseCmdline(body string) string {
	parts := strings.Split(body, "\x00")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// parseIO pulls read_bytes/write_bytes from /proc/<pid>/io.
//
// read_bytes, not rchar: rchar counts page-cache reads that never touched
// a disk, which would make a memory-cached workload look IO-heavy.
func parseIO(body string) (read, write uint64) {
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "read_bytes:":
			read = v
		case "write_bytes:":
			write = v
		}
	}
	return read, write
}

// parseStatus reads the fields the report needs out of /proc/<pid>/status.
//
// Also classifies the process, because a zero RSS has three unrelated
// causes and they look identical in a table. A kernel thread has no VmRSS
// line at all — no user address space exists to measure — and a zombie's
// address space is already torn down. Neither is an idle process holding
// almost no memory, and neither can be usefully killed.
func parseStatus(pid int, body string) Proc {
	p := Proc{PID: pid}
	var state string
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "Name:":
			p.Name = f[1]
		case "State:":
			state = f[1]
		case "PPid:":
			p.PPID, _ = strconv.Atoi(f[1])
		case "VmRSS:":
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			p.RSSBytes = kb * 1024
		}
	}
	// Zombie is decided here because status is where the state lives. The
	// kernel-thread test needs /proc/<pid>/cmdline and so happens in
	// Snapshot; a zombie found here wins, since "already exited, waiting
	// to be reaped" is the state the operator can act on.
	if state == "Z" {
		p.Kind = KindZombie
	}
	return p
}
