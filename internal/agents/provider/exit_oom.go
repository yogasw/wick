package provider

import "fmt"

// exit_oom.go builds the human sentence behind ExitOOM. It lives apart
// from agent.go because the wording is the whole product here: the reason
// exists to replace "agent stopped" with something an operator can act on.

// OOMDetail builds the human sentence for an OOM kill.
//
// It names both numbers on purpose. "Agent stopped" gives an operator
// nothing to act on; a measured peak beside the ceiling it broke points
// straight at the setting to change.
//
// peakBytes 0 means the scope was reaped before it could be read — say
// nothing about the peak rather than report a zero that never happened.
func OOMDetail(peakBytes uint64, limitMB int) string {
	if peakBytes == 0 {
		return fmt.Sprintf(
			"killed by the kernel for exceeding its %d MB memory limit. "+
				"Raise the limit in provider settings, or split the work into smaller steps.",
			limitMB)
	}
	return fmt.Sprintf(
		"used %s, over its %d MB limit. "+
			"Raise the limit in provider settings, or split the work into smaller steps.",
		humanBytes(peakBytes), limitMB)
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
