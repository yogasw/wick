//go:build windows

package updater

import "syscall"

// detachedSysProcAttr makes the helper survive our os.Exit(0).
// CREATE_NEW_PROCESS_GROUP detaches from our console group so a
// CTRL+C delivered to us doesn't propagate; DETACHED_PROCESS gives
// it no console at all (the helper is a .bat that doesn't need one
// — its output goes to the helper log file).
//
// HideWindow keeps `cmd /c start` from flashing a console window on
// the user's screen.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
}
