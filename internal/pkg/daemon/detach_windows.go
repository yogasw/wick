//go:build windows

package daemon

import "syscall"

// detachAttr fully detaches the spawned daemon from the caller's
// console:
//
//	DETACHED_PROCESS          (0x00000008) — no inherited console
//	CREATE_NEW_PROCESS_GROUP  (0x00000200) — Ctrl+C in parent won't
//	                                          propagate to the child
//
// HideWindow is set so the daemon doesn't briefly flash a console
// window during startup.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200,
		HideWindow:    true,
	}
}
