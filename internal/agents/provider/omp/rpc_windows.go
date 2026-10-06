//go:build windows

package omp

import (
	"strconv"

	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// signalGroup kills the RPC process's process tree.
func signalGroup(pid int, force bool) {
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
