package provider

import "fmt"

// exit_oom.go builds the human sentence behind ExitOOM. It lives apart
// from agent.go because the wording is the whole product here: the reason
// exists to replace "agent stopped" with something an operator can act on.

// OOMDetail builds the human sentence for an OOM kill.
//
// It names whatever numbers it actually has, on purpose. "Agent stopped"
// gives an operator nothing to act on; a measured peak beside the ceiling
// it broke points straight at the setting to change.
//
// Two values may be missing, and neither is invented:
//
//   - peakBytes 0: the scope was reaped before it could be read. Say
//     nothing about the peak rather than report a zero that never happened.
//   - limitMB 0: this agent has NO per-agent ceiling, so the kill came from
//     the aggregate slice limit or from the machine running out entirely.
//     Printing "its 0 MB limit" would be worse than saying nothing — it
//     reads as a misconfiguration that does not exist, and sends the
//     operator to a setting that is not the cause.
func OOMDetail(peakBytes uint64, limitMB int) string {
	const raise = "Raise the limit in provider settings, or split the work into smaller steps."
	// No per-agent ceiling: the cause is the combined limit or the machine
	// itself, so point at those instead of a per-agent setting.
	const shared = "This agent has no individual limit, so the cause is the combined limit " +
		"for all agents or the machine running out of memory."

	switch {
	case limitMB <= 0 && peakBytes == 0:
		return "killed by the kernel for running out of memory. " + shared
	case limitMB <= 0:
		return fmt.Sprintf("used %s and was killed by the kernel for running out of memory. %s",
			humanBytes(peakBytes), shared)
	case peakBytes == 0:
		return fmt.Sprintf("killed by the kernel for exceeding its %d MB memory limit. %s",
			limitMB, raise)
	default:
		return fmt.Sprintf("used %s, over its %d MB limit. %s",
			humanBytes(peakBytes), limitMB, raise)
	}
}

// humanBytes renders a byte count the way an operator reads it.
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGT"[exp])
}
