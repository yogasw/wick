//go:build windows

package processctl

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// ProcessInfo holds best-effort metadata about a running OS process.
// Fields are empty/zero when unreadable (process owned by another user).
type ProcessInfo struct {
	PID  int
	UID  int    // always -1 on Windows (not used for ownership check)
	Exe  string // full path from QueryFullProcessImageName; "" = unreadable
	Name string // basename of Exe; "" = unreadable
}

// Verified returns true when the PID's exe (or basename) matches
// exePath. Nothing readable → false, so a recycled PID owned by another
// process is never mistaken for ours. Case-insensitive on Windows.
func (p ProcessInfo) Verified(exePath string) bool {
	if p.Exe != "" {
		return strings.EqualFold(p.Exe, exePath)
	}
	if p.Name != "" {
		return strings.EqualFold(p.Name, filepath.Base(exePath))
	}
	return false
}

// QueryProcess reads available metadata for pid via the Win32 API.
func QueryProcess(pid int) ProcessInfo {
	info := ProcessInfo{PID: pid, UID: -1}
	if pid <= 0 {
		return info
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION|0x0010 /* PROCESS_VM_READ */, false, uint32(pid))
	if err != nil {
		return info
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	proc := syscall.MustLoadDLL("kernel32.dll").MustFindProc("QueryFullProcessImageNameW")
	r, _, _ := proc.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		info.Exe = syscall.UTF16ToString(buf[:size])
		info.Name = filepath.Base(info.Exe)
	}
	return info
}

// ProcessAlive reports whether pid refers to a running process.
// OpenProcess + GetExitCodeProcess == STILL_ACTIVE is the canonical
// Windows liveness check.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const stillActive = 259
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
