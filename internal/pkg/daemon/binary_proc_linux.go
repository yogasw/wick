//go:build linux

package daemon

import (
	"bytes"
	"os"
	"strconv"
)

// procCmdline0 returns argv[0] of a running process, or "" when it cannot
// be read (permissions, or the process is gone).
func procCmdline0(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return ""
	}
	if i := bytes.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	return string(bytes.TrimSpace(raw))
}

// procCwd returns a process's working directory, used to resolve a relative
// argv[0] the way the kernel did when it started.
func procCwd(pid int) string {
	cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return ""
	}
	return cwd
}
