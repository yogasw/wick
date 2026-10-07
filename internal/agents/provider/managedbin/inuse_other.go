//go:build !linux

package managedbin

// scanInUse has no portable equivalent of /proc/<pid>/exe; report
// nothing, so only current is protected from removal off Linux.
func scanInUse(string) map[string]int { return map[string]int{} }
