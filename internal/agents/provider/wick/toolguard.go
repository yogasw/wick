package wick

import (
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// toolguard.go bounds the shell commands the wick provider runs itself.
//
// The wick provider is in-process, but it is not memory-free: it spawns
// real subprocesses for its shell tool. A `grep -r` over a large tree or
// a `cat` of a huge file is an unguarded direct child of wick — the same
// failure mode as a ballooning agent, with none of the isolation.
//
// Unlike an agent subprocess, a tool that exceeds its limit must NOT take
// the agent down: the call fails, the model reads the error, and it can
// retry with a narrower scope. That recoverability is what makes a tight
// ceiling here reasonable — a grep does not need an agent's budget.

var toolSeq atomic.Int64

// nextToolSeq yields a unique scope suffix; systemd refuses a duplicate
// unit name while the first is still alive.
func nextToolSeq() int { return int(toolSeq.Add(1)) }

// wrapToolCmd bounds one tool subprocess.
//
// limitMB 0 leaves the command exactly as it was, which is both the
// default and what an operator gets when the guard is off.
func wrapToolCmd(bin string, args []string, limitMB int, seq int) (string, []string, string) {
	if limitMB <= 0 || !memscope.Available() {
		return bin, args, ""
	}
	unit := "wick-tool-" + strconv.Itoa(seq)
	wbin, wargv := memscope.WrapArgv(bin, args, memscope.Opts{
		Unit: unit, Slice: memscope.SliceName, LimitMB: limitMB,
	})
	return wbin, wargv, unit
}

// toolOOMKilled reports whether a finished tool subprocess was killed for
// memory. An empty unit (unwrapped) is never an OOM.
func toolOOMKilled(unit string) bool {
	if unit == "" {
		return false
	}
	st := memscope.ReadStats(unit)
	return st.Known && st.OOMKills > 0
}

// toolOOMMessage is what the model sees. It names the ceiling and points
// at the fix, because the model is the one that decides what to try next —
// a bare "command failed" would just get retried unchanged.
func toolOOMMessage(limitMB int) string {
	return fmt.Sprintf(
		"command stopped: it exceeded the %d MB memory limit for tool commands. "+
			"Retry with a narrower scope (fewer files, a smaller range, or streamed output).",
		limitMB)
}
