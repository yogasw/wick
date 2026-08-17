//go:build windows

package sysmem

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// sysmem_windows.go reads machine memory on Windows.
//
// The memory GUARD stays Linux-only — it needs cgroups — but the machine
// TOTAL is needed by anything that expresses a number as a share ("chrome
// is 12% of this box"). Without it those columns silently read 0%, which
// looks like a broken feature rather than an unsupported platform.

// memoryStatusEx mirrors MEMORYSTATUSEX. Declared here because
// x/sys/windows does not export it.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

var (
	modkernel32sysmem        = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = modkernel32sysmem.NewProc("GlobalMemoryStatusEx")
)

func read() (total, available uint64) {
	var st memoryStatusEx
	st.length = uint32(unsafe.Sizeof(st))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return 0, 0
	}
	// availPhys is physical memory a new allocation can take — the closest
	// analogue to MemAvailable on Linux, and the number admission cares
	// about.
	return st.totalPhys, st.availPhys
}

// Total reports total physical RAM in bytes; ok is false when unknown.
func Total() (uint64, bool) {
	t, _ := read()
	return t, t > 0
}

// Available reports physical memory available for a new allocation.
func Available() (uint64, bool) {
	_, a := read()
	return a, a > 0
}
