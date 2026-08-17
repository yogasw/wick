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

// parseStatus reads the three fields the report needs out of
// /proc/<pid>/status. A kernel thread has no VmRSS line and reads as 0.
func parseStatus(pid int, body string) Proc {
	p := Proc{PID: pid}
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "Name:":
			p.Name = f[1]
		case "PPid:":
			p.PPID, _ = strconv.Atoi(f[1])
		case "VmRSS:":
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			p.RSSBytes = kb * 1024
		}
	}
	return p
}
