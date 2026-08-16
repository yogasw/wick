package sysmem

import "testing"

// MemAvailable is the kernel's own estimate of what a new process can
// get. MemFree is not a substitute — it excludes reclaimable cache and
// would refuse spawns on a perfectly healthy machine.
func TestParseMeminfo(t *testing.T) {
	body := "MemTotal:        3082240 kB\n" +
		"MemFree:          123456 kB\n" +
		"MemAvailable:    1258291 kB\n" +
		"Buffers:           12345 kB\n"

	total, avail := parseMeminfo(body)
	if total != 3082240*1024 {
		t.Fatalf("total = %d, want %d", total, uint64(3082240)*1024)
	}
	if avail != 1258291*1024 {
		t.Fatalf("available = %d, want %d", avail, uint64(1258291)*1024)
	}
}

// A kernel without MemAvailable (very old) must report zero rather than
// silently falling back to MemFree, which is a different number wearing
// the same name.
func TestParseMeminfo_NoAvailableField(t *testing.T) {
	_, avail := parseMeminfo("MemTotal: 100 kB\nMemFree: 50 kB\n")
	if avail != 0 {
		t.Fatalf("available = %d, want 0 when MemAvailable is absent", avail)
	}
}

// Garbage must not panic or produce a number out of thin air.
func TestParseMeminfo_Malformed(t *testing.T) {
	total, avail := parseMeminfo("MemTotal:\nMemAvailable: abc kB\nnonsense\n")
	if total != 0 || avail != 0 {
		t.Fatalf("malformed input produced total=%d avail=%d, want 0/0", total, avail)
	}
}
