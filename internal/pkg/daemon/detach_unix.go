//go:build !windows

package daemon

import "syscall"

// detachAttr makes the spawned daemon a session leader (Setsid)
// so it survives after the parent shell exits and ignores SIGHUP
// from a closed terminal.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
