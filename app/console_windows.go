//go:build windows

package app

import (
	"os"
	"syscall"
)

// init attaches stdout/stderr/stdin to the parent console when the binary
// is launched from cmd / PowerShell. Built with -H=windowsgui (default
// for non-headless windows builds), Go would otherwise leave the standard
// streams detached, swallowing all output.
//
// Called automatically because the package is imported by every wick app.
// No-op when launched from Explorer (no parent console) — falls through
// silently and stdout stays nil, which matches GUI expectations.
func init() {
	const attachParentProcess = ^uintptr(0) // -1
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	if r, _, _ := kernel32.NewProc("AttachConsole").Call(attachParentProcess); r == 0 {
		return
	}
	if h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && h != 0 {
		os.Stdout = os.NewFile(uintptr(h), "stdout")
	}
	if h, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE); err == nil && h != 0 {
		os.Stderr = os.NewFile(uintptr(h), "stderr")
	}
	if h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE); err == nil && h != 0 {
		os.Stdin = os.NewFile(uintptr(h), "stdin")
	}
}
