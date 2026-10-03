package resourceguard

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
)

// Host is everything the guard reads from and does to the machine. The
// Linux implementation reads /proc and the cgroup v2 tree under
// agents.slice; tests drive the engine with a fake so no real process is
// ever signalled.
type Host interface {
	// MemAvailable is MemAvailable and MemTotal from /proc/meminfo, in MB.
	MemAvailable() (availMB, totalMB int, ok bool)
	// PSI is the avg10 "some" and "full" pressure for memory or cpu.
	PSI(resource string) (some10, full10 float64, ok bool)
	// CPUTimes is the host-wide busy and total jiffies from /proc/stat
	// (busy = user+nice+system+irq+softirq+steal; total adds idle+iowait),
	// so busy % is a delta between two samples — not a load average.
	CPUTimes() (busy, total uint64, ok bool)
	// ProcsRunning is /proc/stat procs_running: tasks runnable right now.
	ProcsRunning() int
	NumCPU() int
	// Scopes lists the agent scopes under agents.slice with their memory
	// and member processes.
	Scopes() []Scope
	// Proc reads one process; ok=false when it is gone.
	Proc(pid int) (Proc, bool)
	// SetSliceCPU writes agents.slice's cpu.max (pct of one core; 0 =
	// uncapped), cpu.weight and pids.max (0 = leave as is).
	SetSliceCPU(quotaPct, weight, tasksMax int) error
	// Freeze freezes or thaws one scope through cgroup.freeze.
	Freeze(scope string, frozen bool) error
	// KillScope kills every process in a scope through cgroup.kill.
	KillScope(scope string) error
	Signal(pid int, sig syscall.Signal) error
	// SelfScope is the cgroup the wick daemon itself runs in; never acted on.
	SelfScope() string
}

// Scope is one agent's cgroup.
type Scope struct {
	Name     string // e.g. claude-agent-1110-4.scope, or app.slice/run-u12.service
	MemBytes uint64
	PIDs     []int
	// Detached marks a transient run-* unit an agent started with
	// `systemd-run --user` outside agents.slice. It has no agent CLI of
	// its own — every process in it is disposable.
	Detached bool
}

// Proc is one process as the guard sees it.
type Proc struct {
	PID      int
	PPID     int
	Comm     string
	Cmdline  string
	RSSBytes uint64
	CPUTicks uint64 // utime+stime, in clock ticks
}

// scopeDirIn resolves a scope name to its directory: a plain name under
// agents.slice, or app.slice/run-* under the user manager. Anything else
// is refused, so a bad name can never reach another cgroup.
func scopeDirIn(user, slice, scope string) (string, error) {
	if rest, ok := strings.CutPrefix(scope, "app.slice/"); ok {
		if strings.HasPrefix(rest, "run-") && !strings.ContainsAny(rest, "/\x00") {
			return filepath.Join(user, "app.slice", rest), nil
		}
		return "", fmt.Errorf("resourceguard: bad scope %q", scope)
	}
	if scope == "" || strings.ContainsAny(scope, "/\x00") || scope == "." || scope == ".." {
		return "", fmt.Errorf("resourceguard: bad scope %q", scope)
	}
	return filepath.Join(slice, scope), nil
}
