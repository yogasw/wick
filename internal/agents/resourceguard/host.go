package resourceguard

import "syscall"

// Host is everything the guard reads from and does to the machine. The
// Linux implementation reads /proc and the cgroup v2 tree under
// agents.slice; tests drive the engine with a fake so no real process is
// ever signalled.
type Host interface {
	// MemAvailable is MemAvailable and MemTotal from /proc/meminfo, in MB.
	MemAvailable() (availMB, totalMB int, ok bool)
	// PSI is the avg10 "some" and "full" pressure for memory or cpu.
	PSI(resource string) (some10, full10 float64, ok bool)
	// Load1 is the one-minute load average; NumCPU the online cores.
	Load1() float64
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
	Name     string // e.g. claude-agent-1110-4.scope
	MemBytes uint64
	PIDs     []int
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
