//go:build windows

package daemon

import (
	"os"
	"syscall"
	"unsafe"
)

// processAlive returns true if pid refers to a running process. On
// Windows, os.FindProcess actually verifies process existence (unlike
// Unix), so a successful FindProcess + GetExitCodeProcess STILL_ACTIVE
// is the canonical check.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE — process has not terminated
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}

// processExePath returns the executable path of pid using
// QueryFullProcessImageName, or "" on error.
func processExePath(pid int) string {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION|0x0410 /* PROCESS_VM_READ */, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	proc := syscall.MustLoadDLL("kernel32.dll").MustFindProc("QueryFullProcessImageNameW")
	r, _, _ := proc.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

// signalProcess on Windows uses Process.Kill for SIGTERM/SIGKILL —
// the signal package only supports os.Kill semantics there. A
// graceful console-event approach (CTRL_BREAK_EVENT) would require
// allocating a console; not worth the complexity for the daemon's
// limited stop path.
func signalProcess(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
