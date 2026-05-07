//go:build windows

package app

import (
	"os"
	"syscall"
)

// init re-binds os.Stdout/Stderr/Stdin to the freshly attached console
// when the process was launched into one (e.g. Explorer double-click
// allocates a new console for a console-subsystem binary). Console-aware
// parents — cmd, PowerShell, msys2, MCP-client pipes — already wired the
// streams before main, and AttachConsole returns 0 in that case so we
// fall through without touching them.
//
// systemtray.Run calls FreeConsole right after dispatch, so the visible
// console window from an Explorer launch only flashes for the few ms it
// takes to reach tray init.
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
