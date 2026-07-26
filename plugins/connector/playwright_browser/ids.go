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
// tree). Best-effort: an already-dead PID is not an error.
//
// It tries a GRACEFUL shutdown first, then force-kills only if that doesn't take.
// This matters for CloakBrowser: a force-kill (taskkill /F, kill -9) leaves the
// licensed session slot occupied until it times out (~15 min), so hard-killing
// every close would exhaust a small plan's seats. A graceful signal lets Chrome
// release the slot immediately; /F is the fallback for a hung process.
func killPID(pid int) {
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		// Graceful first: taskkill WITHOUT /F asks the process to close (WM_CLOSE
		// to the window / console), which lets Chrome exit cleanly and free the
		// session slot. Then /F /T reaps anything left of the tree.
		_ = safeexec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T").Run()
		time.Sleep(1500 * time.Millisecond)
		_ = safeexec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		// Interrupt first (clean exit → slot released), then hard Kill as fallback.
		_ = p.Signal(os.Interrupt)
		time.Sleep(1500 * time.Millisecond)
		_ = p.Kill()
	}
}
