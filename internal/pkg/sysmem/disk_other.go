//go:build !linux && !android && !darwin && !windows

package sysmem

// Disk reports unknown on platforms with neither statfs nor the Windows
// volume API. Callers render "unknown" rather than a zeroed row.
func Disk(path string) (DiskUsage, bool) { return DiskUsage{Path: path}, false }
