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
