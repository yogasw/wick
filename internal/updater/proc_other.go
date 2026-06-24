//go:build !windows

package updater

import "syscall"

// detachedSysProcAttr is a no-op stub on non-Windows; the detached
// helper-process flow is Windows-only (Linux/macOS swap in place via
// syscall.Exec). Kept so swapWindows compiles unconditionally.
func detachedSysProcAttr() *syscall.SysProcAttr { return nil }
