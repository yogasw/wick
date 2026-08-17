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

// Percentage alone gets this wrong in both directions, so both signals
// must agree before the UI raises an alarm.
func TestDiskUsage_Pressure(t *testing.T) {
	const gb = uint64(1) << 30

	cases := []struct {
		name  string
		total uint64
		avail uint64
		want  string
	}{
		// The case that started this: a normal laptop disk. 93% full reads
		// alarming, but 22 GB free is not a problem — an alarm here is one
		// operators learn to ignore.
		{"big disk, high percent, plenty free", 328 * gb, 22 * gb, PressureOK},
		// The inverse: a modest volume that really is nearly out.
		// 20 GB at 1 GB free = 95% used, under the 2 GB floor.
		{"small disk, high percent, nearly out", 20 * gb, 1 * gb, PressureFull},
		// 40 GB at 6 GB free = 85% used, under the 10 GB warn floor but
		// above the 2 GB full floor.
		{"warn band: past 80% and under 10 GB", 40 * gb, 6 * gb, PressureWarn},
		// Past 90% but with room to spare in absolute terms — the laptop
		// case again, one band up.
		{"past 90% but 15 GB free", 200 * gb, 15 * gb, PressureOK},
		// Low occupancy can never be an alarm, however small the disk.
		{"half empty", 100 * gb, 50 * gb, PressureOK},
		// Unknown disk must not render as full.
		{"unknown", 0, 0, PressureOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := DiskUsage{
				TotalBytes: c.total,
				FreeBytes:  c.avail,
				AvailBytes: c.avail,
			}
			if got := d.Pressure(); got != c.want {
				t.Fatalf("Pressure = %q, want %q (%.0f%% used, %d GB free)",
					got, c.want, d.UsedPct(), c.avail/gb)
			}
		})
	}
}

// Pressure reads Available, not Free: the root-reserved slice is not room
// wick can use, so counting it would understate the pressure.
//
// 100 GB with 5 GB free is 95% used — past the percentage gate either
// way. What decides the level is the absolute figure, and the two differ:
// 5 GB free would clear the 2 GB floor, while the 1 GB actually available
// does not.
func TestDiskUsage_PressureUsesAvailable(t *testing.T) {
	const gb = uint64(1) << 30
	d := DiskUsage{
		TotalBytes: 100 * gb,
		FreeBytes:  5 * gb, // would read as merely "warn"
		AvailBytes: 1 * gb, // what an unprivileged process actually gets
	}
	if got := d.Pressure(); got != PressureFull {
		t.Fatalf("Pressure = %q, want %q — it is reading Free instead of Available", got, PressureFull)
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
