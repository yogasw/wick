// Package memscope places each agent spawn in its own systemd transient
// scope inside a sibling slice, so the kernel's memory ceiling applies to
// that agent's whole process tree — grandchildren included — and a kill
// reaches only that agent.
//
// The slice is a SIBLING of wick's own service, never a child of it. That
// placement is the point: agent memory never counts toward wick's cgroup,
// so wick is never the fat process the OOM killer picks, and an aggregate
// ceiling on the slice can never put wick inside the blast radius.
//
// Everything here is user-scope — no root, no sudo, no change to wick's
// own unit.
package memscope

import (
	"fmt"
	"strconv"
	"strings"
)

// SliceName is the systemd slice every agent scope is placed in. The
// cgroupfs fallback (cgroupfs_linux.go) reuses the same name as a plain
// directory — one vocabulary for "the group agents share", whichever
// mechanism is placing them there.
const SliceName = "agents.slice"

// AgentExecSubcommand is the hidden CLI subcommand (`wick __agent-exec`,
// wired in app/agentexec_cmd.go) that WrapArgvCgroupFS re-execs wick
// through. Nothing on a systemd-less host performs "create a cgroup, put
// this process in it, then exec the real binary" for us the way
// systemd-run does, so wick's own binary is the wrapper.
const AgentExecSubcommand = "__agent-exec"

// SliceLimits are the resource controls written onto agents.slice —
// shared by every agent, enforced by the kernel. Zero means "leave the
// kernel default" for every field, so a zero SliceLimits renders a slice
// that groups agents for measurement and constrains nothing.
//
// Memory is the only control that kills; the others shape contention:
//
//   - CPUWeight biases the scheduler when CPU is CONTENDED — agents yield
//     to wick under load, use everything when idle. Never caps.
//   - CPUQuotaPct hard-caps combined agent CPU (100 = one full core).
//     Off by default: a cap slows legitimate heavy work even when the
//     machine is otherwise idle, which is usually not what anyone wants.
//   - TasksMax bounds total processes/threads. This is the fork-bomb
//     guard — thousands of tiny processes can cripple the scheduler while
//     staying comfortably under every memory ceiling.
//   - IOWeight biases block-IO the way CPUWeight biases CPU.
type SliceLimits struct {
	AggregateMB int
	CPUWeight   int // 1..10000, kernel default 100
	CPUQuotaPct int // percent of one core; 0 = uncapped
	TasksMax    int
	IOWeight    int // 1..10000, kernel default 100
}

// Opts describes one scope to create.
type Opts struct {
	Unit    string // transient unit name, unique while alive
	Slice   string // defaults to SliceName when empty
	LimitMB int    // 0 = no MemoryMax (measure mode)
}

// Stats is what a scope's cgroup files report about it.
//
// Known distinguishes "read it, saw no kill" from "could not read it".
// The difference matters: --collect reaps a scope the moment its last
// process exits, so a reader arriving late finds nothing, and reporting
// that as "not an OOM" would silently mislabel the exact failure this
// package exists to explain.
type Stats struct {
	PeakBytes uint64
	OOMKills  int
	Known     bool
}

// ScopeUnitName builds a per-spawn unit name that identifies the provider
// and stays unique for as long as the scope lives.
func ScopeUnitName(provider string, seq int) string {
	return provider + "-agent-" + strconv.Itoa(seq)
}

// WrapArgv returns the binary and argv that run cmd inside a new scope.
//
// MemoryHigh=infinity and MemorySwapMax=0 are always set, at every limit
// including none. MemoryHigh throttles allocation instead of killing —
// a process past it stalls indefinitely while holding its slot, which is
// how one production incident became a 116-minute outage rather than a
// clean kill. Swap does the same more slowly. Neither is a tuning knob.
func WrapArgv(bin string, args []string, o Opts) (string, []string) {
	slice := o.Slice
	if slice == "" {
		slice = SliceName
	}
	argv := []string{
		"--user", "--scope", "--quiet", "--collect",
		"--slice=" + slice,
		"--unit=" + o.Unit,
		"-p", "MemoryHigh=infinity",
		"-p", "MemorySwapMax=0",
	}
	if o.LimitMB > 0 {
		argv = append(argv, "-p", "MemoryMax="+strconv.Itoa(o.LimitMB)+"M")
	}
	argv = append(argv, "--", bin)
	argv = append(argv, args...)
	return "systemd-run", argv
}

// RenderSlice returns the slice unit body carrying the configured
// controls. Every zero field is omitted, so a zero SliceLimits renders a
// slice that groups agents for measurement and constrains nothing.
func RenderSlice(l SliceLimits) string {
	var b strings.Builder
	b.WriteString("[Unit]\n" +
		"Description=Agent sessions (claude, codex, ...), isolated from the wick daemon\n" +
		"Before=slices.target\n" +
		"\n" +
		"[Slice]\n")
	if l.AggregateMB > 0 {
		fmt.Fprintf(&b, "MemoryMax=%dM\n", l.AggregateMB)
	}
	// See WrapArgv: throttling is not an acceptable substitute for killing.
	b.WriteString("MemoryHigh=infinity\nMemorySwapMax=0\n")
	if l.CPUWeight > 0 {
		fmt.Fprintf(&b, "CPUWeight=%d\n", l.CPUWeight)
	}
	if l.CPUQuotaPct > 0 {
		fmt.Fprintf(&b, "CPUQuota=%d%%\n", l.CPUQuotaPct)
	}
	if l.TasksMax > 0 {
		fmt.Fprintf(&b, "TasksMax=%d\n", l.TasksMax)
	}
	if l.IOWeight > 0 {
		fmt.Fprintf(&b, "IOWeight=%d\n", l.IOWeight)
	}
	return b.String()
}
