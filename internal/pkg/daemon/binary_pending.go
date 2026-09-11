package daemon

import (
	"os"
	"path/filepath"

	"github.com/yogasw/wick/pkg/safeexec"
)

// Pending describes a build installed at the daemon's exec path that the
// running process is not the image of — the gap between "new binary in place"
// and "reload". Nothing else reports that state: the app looks perfectly
// normal while the version an operator believes they deployed is not the one
// answering.
type Pending struct {
	Path    string // where the waiting binary sits
	Version string // its app version
	Built   string // its build timestamp
	Size    int64
	ModTime int64 // unix seconds, used to tell a finished copy from one in progress
}

// ServingBinaryPath resolves the path a successor would exec: argv[0] through
// PATH, the same way the handover resolves it.
//
// Deliberately NOT /proc/self/exe — once the file has been replaced that still
// points at the old, now-unlinked inode, which is exactly the thing we are
// trying to detect a difference from.
func ServingBinaryPath() string {
	argv0 := os.Args[0]
	if argv0 != "" {
		if filepath.IsAbs(argv0) {
			return argv0
		}
		if p, err := safeexec.LookPath(argv0); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return ""
}

// PendingSwap compares the binary at the exec path with the running version.
// A differing version OR a differing build timestamp counts: rebuilding the
// same version number is the normal case on a host that deploys from its own
// tree, and reporting "nothing waiting" there would be wrong.
//
// The file is inspected, never executed.
func PendingSwap(runningVersion, runningBuiltAt string) (Pending, bool) {
	path := ServingBinaryPath()
	if path == "" {
		return Pending{}, false
	}
	info, err := InspectBinary(path)
	if err != nil || !info.IsWick() {
		return Pending{}, false
	}
	same := info.AppVersion == runningVersion &&
		(info.BuildTime == "" || runningBuiltAt == "" || info.BuildTime == runningBuiltAt)
	if same {
		return Pending{}, false
	}
	return Pending{
		Path:    path,
		Version: info.AppVersion,
		Built:   info.BuildTime,
		Size:    info.Size,
		ModTime: info.ModTime.Unix(),
	}, true
}
