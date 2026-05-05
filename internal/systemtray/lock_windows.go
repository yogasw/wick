//go:build windows && !headless

package systemtray

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	maxPath                        = 1024
)

// isOurProcessAlive returns true when pid points to a live process
// whose executable basename matches ours. Stale PIDs from crashed
// instances or recycled by unrelated processes return false so the
// caller can claim the slot.
func isOurProcessAlive(pid int) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	want := strings.EqualFold(filepath.Base(exe), filepath.Base(getProcessExe(pid)))
	return want
}

func getProcessExe(pid int) string {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	closeHandle := kernel32.NewProc("CloseHandle")
	queryImageName := kernel32.NewProc("QueryFullProcessImageNameW")

	h, _, _ := openProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer closeHandle.Call(h)

	var buf [maxPath]uint16
	size := uint32(len(buf))
	r, _, _ := queryImageName.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}
