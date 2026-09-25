package agentmemory

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Resources is the footprint of one running backend: what the process holds in
// RAM and what its store holds on disk.
//
// It exists because the honest objection to a memory daemon is its cost — a
// backend at ~130-190 MB RSS on a 3.6 GB host is a real share of the machine
// (PLAN §12.6), and a store that is never pruned grows without anyone
// watching. Both numbers belong on the Overview where a decision can be made
// from them, not buried (PLAN §18.5).
type Resources struct {
	// PID is the daemon wick spawned, 0 when the process isn't ours.
	PID int `json:"pid,omitempty"`
	// RSSBytes is that process's resident set. 0 with RSSKnown false means
	// nobody could measure it — not that it is free.
	RSSBytes uint64 `json:"rss_bytes"`
	RSSKnown bool   `json:"rss_known"`
	// DataDir is the store that was measured, DataDirBytes its total size on
	// disk. That is the whole directory — the SQLite file plus the wiki
	// markdown and raw event files — so it runs larger than the
	// database_bytes in the store status, which counts only the database.
	DataDir      string `json:"data_dir,omitempty"`
	DataDirBytes int64  `json:"data_dir_bytes"`
	DataDirKnown bool   `json:"data_dir_known"`
}

// probeResources measures what it can and reports what it could not, rather
// than returning a zero that reads like "costs nothing".
func probeResources(pid int, dataDir string) Resources {
	r := Resources{PID: pid, DataDir: dataDir}
	if pid > 0 {
		if rss, ok := processRSS(pid); ok {
			r.RSSBytes, r.RSSKnown = rss, true
		}
	}
	if dataDir != "" {
		if n, ok := dirSize(dataDir); ok {
			r.DataDirBytes, r.DataDirKnown = n, true
		}
	}
	return r
}

// processRSS reads a process's resident set from /proc/<pid>/statm, whose
// second field is the resident page count. Not ok off Linux (no /proc) or once
// the process is gone — the same "/proc may not be readable" honesty the
// memory diagnostics page already applies.
func processRSS(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}

// dirSize totals the regular files under dir. Walk errors on individual
// entries are skipped rather than aborting the total: a store with one
// unreadable file still has a size worth showing, and an approximate number
// beats a blank.
func dirSize(dir string) (int64, bool) {
	if _, err := os.Stat(dir); err != nil {
		return 0, false
	}
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // ditto: the file vanished mid-walk
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return total, false
	}
	return total, true
}
