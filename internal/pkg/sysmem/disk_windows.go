//go:build windows

package sysmem

import (
	"golang.org/x/sys/windows"
)

// Disk reports the capacity of the volume holding path.
//
// Unlike the memory readings — which are Linux-only because they parse
// /proc — disk capacity is available on Windows, so the Resources page
// shows a real disk row on the development platform too.
func Disk(path string) (DiskUsage, bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return DiskUsage{Path: path}, false
	}
	// free = bytes available to THIS user (honours quotas), total/totalFree
	// describe the volume. Mirrors Available vs Free on unix.
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return DiskUsage{Path: path}, false
	}
	return DiskUsage{
		Path:       path,
		TotalBytes: total,
		FreeBytes:  totalFree,
		AvailBytes: free,
	}, total > 0
}
