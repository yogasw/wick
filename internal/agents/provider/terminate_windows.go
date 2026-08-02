//go:build windows

package provider

import (
	"strconv"

	"github.com/yogasw/wick/pkg/safeexec"
)

// Process-tree teardown (Windows).
//
// Windows is wick's primary target, so it does NOT get the second-class
// treatment seen elsewhere — no "process groups are POSIX-only, skip it"
// shortcut. The mechanisms differ, the guarantee does not: a killed agent
// takes its descendants with it.
//
// CREATE_NEW_PROCESS_GROUP makes the child the root of its own group.
// Windows has no SIGTERM, so the graceful phase is best-effort and the
// authoritative teardown is `taskkill /T`, which walks the process tree.

// The process group itself is established at spawn time by
// provider/procgroup, shared by every spawner. This file only tears one
// down.

// terminateGroupGracefully asks a process tree to close.
//
// `taskkill` without /F requests a close rather than forcing it, which
// gives a well-behaved CLI the chance to flush. Returns whether the
// request was dispatched; the caller still escalates after the grace
// window.
func terminateGroupGracefully(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmd := safeexec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T")
	hideConsole(cmd)
	return cmd.Run() == nil
}

// killGroup force-kills a process tree.
//
// /T walks descendants and /F forces them — the Windows equivalent of
// SIGKILL to a process group, and the reason an orphaned MCP server does
// not survive its agent here.
func killGroup(pid int) {
	if pid <= 0 {
		return
	}
	cmd := safeexec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	hideConsole(cmd)
	_ = cmd.Run()
}
