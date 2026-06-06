//go:build !windows

package processctl

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ProcessInfo holds best-effort metadata about a running OS process.
// Fields are empty/zero when unreadable (e.g. process owned by another
// user on Android/Termux where /proc/<pid>/* is permission-denied).
type ProcessInfo struct {
	PID  int
	UID  int    // owner uid; -1 = unreadable
	Exe  string // resolved path to executable; "" = unreadable
	Name string // short process name from /proc/<pid>/comm; "" = unreadable
}

// Verified returns true when enough fields were readable to confirm the
// PID is the process we expect: exe matches exePath, or (exe unreadable)
// the short name matches. Nothing readable → false, so a recycled PID
// owned by another process/user is never mistaken for ours.
func (p ProcessInfo) Verified(exePath string) bool {
	if p.Exe != "" {
		return p.Exe == exePath
	}
	if p.Name != "" {
		return p.Name == processBasename(exePath)
	}
	return false
}

// QueryProcess reads available metadata for pid via procfs. Partial
// results are returned when some files are unreadable; callers check
// individual fields (UID == -1 / empty strings mean "couldn't read").
func QueryProcess(pid int) ProcessInfo {
	info := ProcessInfo{PID: pid, UID: -1}
	if pid <= 0 {
		return info
	}
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		info.Exe = exe
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		info.Name = strings.TrimSpace(string(data))
	}
	if f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if uid, err := strconv.Atoi(fields[1]); err == nil {
						info.UID = uid
					}
				}
				break
			}
		}
		_ = f.Close()
	}
	return info
}

// ProcessAlive reports whether pid refers to a running process. Signal 0
// is a kernel-level liveness probe that delivers nothing; EPERM (exists
// but not ours to signal) still counts as alive.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		return true
	} else {
		return os.IsPermission(err)
	}
}

// processBasename returns the last path component, truncated to 15 chars
// to match the /proc/<pid>/comm format.
func processBasename(path string) string {
	if path == "" {
		return ""
	}
	name := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			name = path[i+1:]
			break
		}
	}
	if len(name) > 15 {
		return name[:15]
	}
	return name
}
