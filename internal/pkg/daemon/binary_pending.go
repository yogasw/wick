package daemon

import (
	"fmt"
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
	// Blocked, when non-empty, is why this build must NOT be applied — the
	// same verdict `reload --binary` reaches, phrased for someone reading the
	// update card. A blocked build is still reported as pending on purpose:
	// an operator who installed a file needs to see that it arrived and why
	// it is going nowhere, which is strictly better than a card that never
	// appears or one that counts down forever.
	Blocked string
	// Want is the smallest version that would be accepted, when the block is
	// about the version. It turns "no" into an instruction.
	Want string
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
	blocked, want := blockReason(info, runningVersion)
	return Pending{
		Path:    path,
		Version: info.AppVersion,
		Built:   info.BuildTime,
		Size:    info.Size,
		ModTime: info.ModTime.Unix(),
		Blocked: blocked,
		Want:    want,
	}, true
}

// blockReason applies the rules `reload --binary` already enforces to a build
// that arrived WITHOUT going through it — somebody's `mv`, a CI copy, a script
// that installs and stops. Until this existed the automatic path was the lax
// one: the CLI refused a candidate the watcher would happily exec.
//
// Two rules, and the first is the one that bites.
//
// A build with NO version cannot be told apart from the process running it —
// `same` compares versions, so "" never equals the running number and the file
// stays pending forever. The watcher then hands over to it at every idle
// moment, the successor inspects the same file, finds it pending again, and
// the host re-execs itself in a loop that reads, on screen, as a swap that
// never finishes. Observed 2026-09-25 on a binary built with plain `go build
// -trimpath`: cmd/go omits `-ldflags` from the build info when -trimpath is
// set, so the `-X …BuildAppVersion` the whole scheme depends on was simply not
// recorded, and `version` printed 0.1.343 while the card showed `0.1.343 → —`.
//
// The second is the downgrade: the CLI blocks it behind --force, so the
// unattended path must not do it silently.
func blockReason(candidate BinaryInfo, runningVersion string) (reason, want string) {
	if candidate.AppVersion == "" {
		return "this build carries no version, so nothing can tell it apart from the one running — " +
			"it would be handed over to again at every idle moment, forever. " +
			"Rebuild with -X <app>.BuildAppVersion=<version> (note: `go build -trimpath` drops -ldflags from the build info, use `wick build`).", nextPatch(runningVersion)
	}
	if cmpVersions(candidate.AppVersion, runningVersion) < 0 {
		return fmt.Sprintf("%s is OLDER than the running %s — a downgrade is never applied automatically. "+
			"Install it deliberately with `reload --binary --force` if that is really what you want.",
			candidate.AppVersion, runningVersion), nextPatch(runningVersion)
	}
	return "", ""
}
