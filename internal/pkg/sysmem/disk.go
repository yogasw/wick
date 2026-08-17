package sysmem

// disk.go reports filesystem capacity.
//
// Capacity is a different failure from IO throughput, and the two are easy
// to conflate: a busy disk makes everything slow, while a FULL disk makes
// writes fail outright. Wick writes continuously — session transcripts,
// spawn logs, trace events — so "how much room is left where the data
// lives" is a question an operator needs answered before the writes start
// failing, not after.
//
// Reported for a caller-supplied path rather than "the disk", because the
// data tree can be relocated (WICK_DATA_DIR) onto a different filesystem
// from the binary.

// DiskUsage is the capacity of the filesystem holding one path.
//
// Available is what a NON-ROOT process may actually use, which is smaller
// than Free on most filesystems: ext4 reserves a percentage for root.
// Reporting Free would tell an unprivileged wick it has room it cannot
// take.
type DiskUsage struct {
	Path       string
	TotalBytes uint64
	FreeBytes  uint64
	AvailBytes uint64
}

// UsedBytes is what is actually occupied.
func (d DiskUsage) UsedBytes() uint64 {
	if d.TotalBytes < d.FreeBytes {
		return 0
	}
	return d.TotalBytes - d.FreeBytes
}

// UsedPct is occupancy as a percentage of total, 0 when unknown.
//
// Computed against Total/Free rather than Available so it matches what
// `df` prints — an operator comparing the two should not see a
// discrepancy they have to explain.
func (d DiskUsage) UsedPct() float64 {
	if d.TotalBytes == 0 {
		return 0
	}
	return float64(d.UsedBytes()) / float64(d.TotalBytes) * 100
}

// Pressure levels for the UI.
const (
	PressureOK   = "ok"
	PressureWarn = "warn"
	PressureFull = "full"
)

// Pressure grades how close this filesystem is to causing failures.
//
// Percentage ALONE is the wrong signal, and gets it wrong in both
// directions. A 328 GB laptop disk at 93% still has 22 GB free — nothing
// is about to fail — while a 20 GB volume at 89% has 2 GB left and very
// nearly is. Judging by absolute free alone is equally wrong: 5 GB left
// on a 6 TB array means something quite different from 5 GB on a 10 GB
// volume.
//
// So both must agree before the UI cries wolf: a level is only reached
// when the disk is BOTH proportionally full AND short of room in absolute
// terms. An alarm that fires on a healthy laptop is an alarm operators
// learn to ignore.
func (d DiskUsage) Pressure() string {
	if d.TotalBytes == 0 {
		return PressureOK
	}
	const (
		gb       = uint64(1) << 30
		fullPct  = 90.0
		warnPct  = 80.0
		fullFree = 2 * gb
		warnFree = 10 * gb
	)
	// AvailBytes, not FreeBytes: the reserved slice is not room wick can
	// use, so counting it would understate the pressure.
	pct := d.UsedPct()
	switch {
	case pct >= fullPct && d.AvailBytes < fullFree:
		return PressureFull
	case pct >= warnPct && d.AvailBytes < warnFree:
		return PressureWarn
	}
	return PressureOK
}
