package sysmem

import (
	"testing"
)

// Used is total minus free, and must not underflow into a nonsense
// petabyte when a filesystem reports free > total (network mounts do).
func TestDiskUsage_Used(t *testing.T) {
	d := DiskUsage{TotalBytes: 100, FreeBytes: 40}
	if got := d.UsedBytes(); got != 60 {
		t.Fatalf("UsedBytes = %d, want 60", got)
	}

	weird := DiskUsage{TotalBytes: 40, FreeBytes: 100}
	if got := weird.UsedBytes(); got != 0 {
		t.Fatalf("free > total produced %d, want 0 rather than an underflow", got)
	}
}

// Percentage is computed against total/free so it matches what `df`
// prints — an operator comparing the two should not have to explain a
// discrepancy.
func TestDiskUsage_UsedPct(t *testing.T) {
	d := DiskUsage{TotalBytes: 200, FreeBytes: 50}
	if got := d.UsedPct(); got != 75 {
		t.Fatalf("UsedPct = %v, want 75", got)
	}
}

// An unknown disk must read as 0%, not divide by zero.
func TestDiskUsage_ZeroTotal(t *testing.T) {
	var d DiskUsage
	if got := d.UsedPct(); got != 0 {
		t.Fatalf("UsedPct on a zero disk = %v, want 0", got)
	}
	if got := d.UsedBytes(); got != 0 {
		t.Fatalf("UsedBytes on a zero disk = %d, want 0", got)
	}
}

// Disk() must report a real filesystem on every platform that supports
// it, and must never report ok with a zero size — a zeroed row would
// render as a full disk.
func TestDisk_ReadsRealFilesystem(t *testing.T) {
	got, ok := Disk(t.TempDir())
	if !ok {
		t.Skip("no filesystem stat on this platform")
	}
	if got.TotalBytes == 0 {
		t.Fatal("Disk reported ok with a zero total — callers would render a full disk")
	}
	if got.FreeBytes > got.TotalBytes {
		t.Fatalf("free %d exceeds total %d", got.FreeBytes, got.TotalBytes)
	}
	// Available is what an unprivileged process may use: never more than free.
	if got.AvailBytes > got.FreeBytes {
		t.Fatalf("available %d exceeds free %d", got.AvailBytes, got.FreeBytes)
	}
}

// A path that does not exist must report unknown, not a zeroed disk that
// reads as 100% full.
func TestDisk_MissingPathIsUnknown(t *testing.T) {
	if _, ok := Disk(t.TempDir() + "/definitely/not/here"); ok {
		t.Fatal("a missing path reported ok")
	}
}
