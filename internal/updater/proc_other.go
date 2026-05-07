//go:build !windows

package updater

import "syscall"

// detachedSysProcAttr is a no-op stub on non-Windows; the helper-script
// flow is Windows-only (Linux uses pkexec dpkg, macOS swaps in-process).
// Kept so swapWindows compiles unconditionally.
func detachedSysProcAttr() *syscall.SysProcAttr { return nil }
