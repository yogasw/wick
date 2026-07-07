package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/safeexec"
)

// newSessionID mints a short, unique-per-host id for a live session. It combines
// the current unix-nano time with the plugin PID so two opens in the same
// process never collide, without needing a rand seed. The instance id is folded
// in so sessions from different connector rows never clash in a shared dir.
func newSessionID(c *connector.Ctx) string {
	inst := c.InstanceID()
	if len(inst) > 6 {
		inst = inst[:6]
	}
	if inst == "" {
		inst = "pw"
	}
	return fmt.Sprintf("%s-%d-%d", inst, os.Getpid(), time.Now().UnixNano())
}

// sessionNow is the timestamp stamped into a session's metadata. Wrapped so the
// call site reads intent (creation time) rather than a bare time.Now().
func sessionNow(_ *connector.Ctx) time.Time { return time.Now() }

// killPID terminates the detached browser process (and, on Windows, its child
// tree). Best-effort: a already-dead PID is not an error.
func killPID(pid int) {
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		// Chrome spawns a tree of child processes; /T kills the whole tree, /F
		// forces it. os.Process.Kill only reaps the parent and orphans children.
		_ = safeexec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
