//go:build !windows && !headless

package systemtray

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// isOurProcessAlive returns true when pid points to a live process
// whose executable basename matches ours. Falls back to a kill -0
// liveness probe when the platform doesn't expose /proc/<pid>/exe
// (macOS), because there's no portable cross-process exe lookup
// without cgo. macOS therefore can't distinguish a same-PID unrelated
// process from a real second instance — acceptable for a soft lock.
func isOurProcessAlive(pid int) bool {
	if !pidAlive(pid) {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	if other, err := os.Readlink("/proc/" + itoa(pid) + "/exe"); err == nil {
		return strings.EqualFold(filepath.Base(exe), filepath.Base(other))
	}
	return true
}

func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// itoa avoids strconv to stop the import list growing for a one-liner.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
