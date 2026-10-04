//go:build windows

package opencode

import (
	"strconv"

	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// signalServerGroup kills the server's process tree.
func signalServerGroup(pid int, force bool) {
	if pid <= 0 {
		return
	}
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	c := safeexec.Command("taskkill", args...)
	c.Env = envscrub.ScrubOSEnv()
	hideConsole(c)
	_ = c.Run()
}
