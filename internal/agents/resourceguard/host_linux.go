//go:build linux

package resourceguard

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// sliceName matches memscope.SliceName; not imported to keep this
// package free of the provider tree.
const sliceName = "agents.slice"

type linuxHost struct {
	user  string // the user manager's cgroup (user@UID.service)
	slice string // absolute path of agents.slice in the cgroup v2 tree
	self  string // scope name the daemon runs in
}

// NewHost finds agents.slice in the cgroup v2 hierarchy. nil when the
// machine has no unified hierarchy or no slice yet — the guard then does
// nothing, exactly like an unsupported platform.
func NewHost() Host {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return nil
	}
	h := &linuxHost{slice: findSlice()}
	if h.slice == "" {
		return nil
	}
	h.user = filepath.Dir(h.slice)
	if b, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		cg := "/sys/fs/cgroup" + strings.TrimSpace(string(b[bytes.LastIndexByte(b, ':')+1:]))
		if rel, err := filepath.Rel(h.slice, cg); err == nil && !strings.HasPrefix(rel, "..") {
			h.self = strings.SplitN(rel, string(filepath.Separator), 2)[0]
		} else if rel, err := filepath.Rel(h.user, cg); err == nil && !strings.HasPrefix(rel, "..") {
			h.self = rel
		}
	}
	return h
}

// findSlice locates agents.slice under the user manager of this uid, the
// place `systemd-run --user --slice=agents.slice` puts it.
func findSlice() string {
	uid := strconv.Itoa(os.Getuid())
	p := filepath.Join("/sys/fs/cgroup/user.slice", "user-"+uid+".slice", "user@"+uid+".service", sliceName)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p
	}
	return ""
}

func (h *linuxHost) MemAvailable() (int, int, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	avail, total := -1, -1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 {
			continue
		}
		kb, _ := strconv.Atoi(fs[1])
		switch fs[0] {
		case "MemAvailable:":
			avail = kb / 1024
		case "MemTotal:":
			total = kb / 1024
		}
	}
	return avail, total, avail >= 0
}

func (h *linuxHost) PSI(resource string) (float64, float64, bool) {
	b, err := os.ReadFile("/proc/pressure/" + resource)
	if err != nil {
		return 0, 0, false
	}
	return parsePSI(string(b))
}

// parsePSI reads avg10 of the "some" and "full" lines.
func parsePSI(s string) (some, full float64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		fs := strings.Fields(line)
		if len(fs) < 2 || !strings.HasPrefix(fs[1], "avg10=") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimPrefix(fs[1], "avg10="), 64)
		if err != nil {
			continue
		}
		switch fs[0] {
		case "some":
			some, ok = v, true
		case "full":
			full = v
		}
	}
	return some, full, ok
}

func (h *linuxHost) CPUTimes() (uint64, uint64, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	return parseCPUTimes(string(b))
}

func (h *linuxHost) ProcsRunning() int {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	return parseProcsRunning(string(b))
}

func (h *linuxHost) NumCPU() int { return runtime.NumCPU() }

func (h *linuxHost) Scopes() []Scope {
	var out []Scope
	if entries, err := os.ReadDir(h.slice); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, readScope(filepath.Join(h.slice, e.Name()), e.Name(), false))
			}
		}
	}
	// Transient units an agent started with `systemd-run --user` land in
	// app.slice, outside every agents.slice limit — a build run that way
	// is exactly what took small hosts down, so they are covered too.
	// Only run-* units: app.slice also holds wick itself and other
	// services the guard has no business touching.
	app := filepath.Join(h.user, "app.slice")
	if entries, err := os.ReadDir(app); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "run-") {
				out = append(out, readScope(filepath.Join(app, e.Name()), "app.slice/"+e.Name(), true))
			}
		}
	}
	return out
}

func readScope(dir, name string, detached bool) Scope {
	s := Scope{Name: name, Detached: detached}
	if b, err := os.ReadFile(filepath.Join(dir, "memory.current")); err == nil {
		s.MemBytes, _ = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	}
	// Nested groups (a tool scope inside an agent) are folded into the
	// agent: the agent is the unit the ladder acts on.
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "cgroup.procs" {
			return nil
		}
		b, _ := os.ReadFile(path)
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil {
				s.PIDs = append(s.PIDs, pid)
			}
		}
		return nil
	})
	return s
}

func (h *linuxHost) Proc(pid int) (Proc, bool) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return Proc{}, false
	}
	p, ok := parseStat(string(stat))
	if !ok {
		return Proc{}, false
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		p.Cmdline = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
	}
	return p, true
}

// parseStat reads pid, comm, ppid, utime+stime and rss from /proc/<pid>/stat.
// comm is parenthesised and may contain spaces, so fields are counted
// from the last ')'.
func parseStat(s string) (Proc, bool) {
	open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || close < open {
		return Proc{}, false
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(s[:open]))
	rest := strings.Fields(s[close+1:])
	// rest[0]=state(3) rest[1]=ppid(4) rest[11]=utime(14) rest[12]=stime(15) rest[21]=rss(24)
	if len(rest) < 22 {
		return Proc{}, false
	}
	ppid, _ := strconv.Atoi(rest[1])
	ut, _ := strconv.ParseUint(rest[11], 10, 64)
	st, _ := strconv.ParseUint(rest[12], 10, 64)
	rss, _ := strconv.ParseUint(rest[21], 10, 64)
	return Proc{
		PID: pid, PPID: ppid, Comm: s[open+1 : close],
		CPUTicks: ut + st, RSSBytes: rss * uint64(os.Getpagesize()),
	}, true
}

// cpuMax renders cgroup v2 cpu.max for a percentage of one core.
func cpuMax(pct int) string {
	if pct <= 0 {
		return "max 100000"
	}
	return strconv.Itoa(pct*1000) + " 100000"
}

func (h *linuxHost) SetSliceCPU(quotaPct, weight, tasksMax int) error {
	var errs []error
	errs = append(errs, os.WriteFile(filepath.Join(h.slice, "cpu.max"), []byte(cpuMax(quotaPct)), 0o644))
	if weight > 0 {
		errs = append(errs, os.WriteFile(filepath.Join(h.slice, "cpu.weight"), []byte(strconv.Itoa(weight)), 0o644))
	}
	if tasksMax > 0 {
		errs = append(errs, os.WriteFile(filepath.Join(h.slice, "pids.max"), []byte(strconv.Itoa(tasksMax)), 0o644))
	}
	return errors.Join(errs...)
}

func (h *linuxHost) scopeDir(scope string) (string, error) {
	return scopeDirIn(h.user, h.slice, scope)
}

func (h *linuxHost) Freeze(scope string, frozen bool) error {
	dir, err := h.scopeDir(scope)
	if err != nil {
		return err
	}
	v := "0"
	if frozen {
		v = "1"
	}
	return os.WriteFile(filepath.Join(dir, "cgroup.freeze"), []byte(v), 0o644)
}

func (h *linuxHost) KillScope(scope string) error {
	dir, err := h.scopeDir(scope)
	if err != nil {
		return err
	}
	// A frozen group must be thawed for the kill to be delivered promptly.
	_ = os.WriteFile(filepath.Join(dir, "cgroup.freeze"), []byte("0"), 0o644)
	return os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0o644)
}

func (h *linuxHost) Signal(pid int, sig syscall.Signal) error {
	if pid <= 1 || pid == os.Getpid() {
		return fmt.Errorf("resourceguard: refusing to signal pid %d", pid)
	}
	return syscall.Kill(pid, sig)
}

func (h *linuxHost) SelfScope() string { return h.self }
