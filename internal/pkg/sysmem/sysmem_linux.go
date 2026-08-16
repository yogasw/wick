//go:build linux || android

package sysmem

import "os"

var meminfoPath = "/proc/meminfo"

func read() (total, available uint64) {
	b, err := os.ReadFile(meminfoPath)
	if err != nil {
		return 0, 0
	}
	return parseMeminfo(string(b))
}

// Total reports total RAM in bytes; ok is false when unknown.
func Total() (uint64, bool) {
	t, _ := read()
	return t, t > 0
}

// Available reports the kernel's estimate of allocatable memory in bytes;
// ok is false when unknown.
func Available() (uint64, bool) {
	_, a := read()
	return a, a > 0
}
