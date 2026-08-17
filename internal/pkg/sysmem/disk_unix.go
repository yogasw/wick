//go:build linux || android || darwin

package sysmem

import "syscall"

// Disk reports the capacity of the filesystem holding path.
//
// ok is false when the path cannot be stat'd — a relocated data dir that
// does not exist yet, or a platform without statfs. Callers must render
// "unknown" rather than a zeroed row, or an unreadable disk looks like an
// empty one.
func Disk(path string) (DiskUsage, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskUsage{Path: path}, false
	}
	// Bsize is the fragment size in bytes; every field below is a count of
	// those blocks. Widened before multiplying — on 32-bit these are
	// uint32 and would overflow on any disk past 4 GB.
	bs := uint64(st.Bsize)
	return DiskUsage{
		Path:       path,
		TotalBytes: uint64(st.Blocks) * bs,
		FreeBytes:  uint64(st.Bfree) * bs,
		AvailBytes: uint64(st.Bavail) * bs,
	}, st.Blocks > 0
}
