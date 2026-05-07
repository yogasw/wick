//go:build !headless

package systemtray

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/userconfig"
)

// acquireSingleInstance is a per-app PID-file lock living next to the
// app's config / DB / logs under ~/.<appName>. Two binaries with
// different appNames don't collide; two launches of the same binary do.
//
// On startup we read instance.pid (if present) and ask the OS whether
// that PID is still a live process whose executable matches ours. A
// hit means "another copy is already running" and the caller bails. A
// miss (no file, dead PID, or unrelated process recycled the same PID)
// means we own the slot — overwrite with our own PID and return a
// release closer that removes the file on graceful shutdown.
func acquireSingleInstance() (release func() error, err error) {
	dir, derr := userconfig.Dir(appName)
	if derr != nil {
		return nil, fmt.Errorf("user config dir: %w", derr)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	pidPath := filepath.Join(dir, "instance.pid")

	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 && pid != os.Getpid() {
			if isOurProcessAlive(pid) {
				return nil, fmt.Errorf("another instance is already running (pid %d)", pid)
			}
		}
	}

	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(pidPath, []byte(pid), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", pidPath, err)
	}
	return func() error { return os.Remove(pidPath) }, nil
}
