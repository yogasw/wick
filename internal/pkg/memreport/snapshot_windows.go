//go:build windows

package memreport

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// snapshot_windows.go enumerates processes on Windows.
//
// This exists so the Resources page is not a blank slab on the platform
// most of this is developed on. The memory GUARD stays Linux-only — it
// needs cgroups — but "which process is eating this machine" is a
// question worth answering wherever wick runs.
//
// Windows has no /proc, so the counters differ from the Linux path:
// working-set size stands in for RSS, kernel+user time for CPU ticks, and
// IO counters come from the process handle. Fields we cannot read stay
// zero rather than being guessed.

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS. Declared here
// because x/sys/windows does not export it.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// ioCounters mirrors IO_COUNTERS.
type ioCounters struct {
	readOperationCount  uint64
	writeOperationCount uint64
	otherOperationCount uint64
	readTransferCount   uint64
	writeTransferCount  uint64
	otherTransferCount  uint64
}

var (
	modpsapi                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = modpsapi.NewProc("GetProcessMemoryInfo")

	modkernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessIoCtrs = modkernel32.NewProc("GetProcessIoCounters")
)

// Snapshot enumerates every process the current token can see.
//
// Processes that cannot be opened (system processes, other users' under a
// non-elevated token) still appear with their name and pid — losing the
// row entirely would hide them from a listing whose whole job is showing
// what is running.
func Snapshot() ([]Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var out []Proc
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))

	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		p := Proc{
			PID:  int(e.ProcessID),
			PPID: int(e.ParentProcessID),
			Name: windows.UTF16ToString(e.ExeFile[:]),
		}
		fillProcessCounters(&p)
		out = append(out, p)
	}
	return out, nil
}

// fillProcessCounters adds memory, CPU, and IO to p when the process can
// be opened. Failures leave the fields at zero: a listing that drops
// unreadable processes would hide exactly the system processes an
// operator is looking for.
func fillProcessCounters(p *Proc) {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ,
		false, uint32(p.PID))
	if err != nil {
		// Retry without VM_READ: enough for CPU and IO on processes that
		// refuse memory access.
		h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
		if err != nil {
			return
		}
	}
	defer windows.CloseHandle(h)

	var mem processMemoryCounters
	mem.cb = uint32(unsafe.Sizeof(mem))
	if r, _, _ := procGetProcessMemoryInfo.Call(
		uintptr(h), uintptr(unsafe.Pointer(&mem)), uintptr(mem.cb)); r != 0 {
		// Working set is the closest analogue to RSS: physical memory the
		// process currently occupies.
		p.RSSBytes = uint64(mem.workingSetSize)
	}

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err == nil {
		// Filetime is 100ns units; Linux ticks are 1/clockTicksPerSec of a
		// second. Convert so CPUPercent's arithmetic holds on both.
		const per100ns = 10_000_000 / clockTicksPerSec
		total := filetimeTo100ns(kernel) + filetimeTo100ns(user)
		p.CPUTicks = total / per100ns
	}

	var io ioCounters
	if r, _, _ := procGetProcessIoCtrs.Call(uintptr(h), uintptr(unsafe.Pointer(&io))); r != 0 {
		// Transfer counts include file-cache traffic, unlike Linux's
		// read_bytes. Closest available; the rate is still meaningful.
		p.IOReadBytes = io.readTransferCount
		p.IOWriteBytes = io.writeTransferCount
	}
}

func filetimeTo100ns(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
