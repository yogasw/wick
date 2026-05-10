package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Sentinel records what an in-flight update is supposed to produce.
// Written by ApplyStagedAndRestart just before the swap; read by the
// next process launch to decide if the update succeeded, partially
// failed, or got stuck. The sentinel is the single source of truth
// for "did the install actually work" — installer exit codes are not
// trusted because msiexec /qn, dpkg postinst, and inner-binary swaps
// all have ways to silently no-op.
type Sentinel struct {
	FromVersion   string    `json:"from_version"`
	ToVersion     string    `json:"to_version"`
	StartedAt     time.Time `json:"started_at"`
	Method        string    `json:"method"` // "msi", "dpkg", "binary-swap"
	InstallerLog  string    `json:"installer_log,omitempty"`
	ExpectedPath  string    `json:"expected_path,omitempty"`
	ExpectedSHA   string    `json:"expected_sha,omitempty"`
	HelperScript  string    `json:"helper_script,omitempty"`
	HelperLog     string    `json:"helper_log,omitempty"`
	OldBinaryPath string    `json:"old_binary_path,omitempty"`
}

const sentinelName = "update-sentinel.json"

// sentinelPath returns the on-disk location of the sentinel file.
func sentinelPath(cacheDir string) string {
	return filepath.Join(cacheDir, sentinelName)
}

// writeSentinel persists s to cacheDir. Failure here aborts the
// update — without a sentinel we have no way to verify success.
func writeSentinel(cacheDir string, s Sentinel) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sentinel: %w", err)
	}
	tmp := sentinelPath(cacheDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write sentinel: %w", err)
	}
	if err := os.Rename(tmp, sentinelPath(cacheDir)); err != nil {
		return fmt.Errorf("rename sentinel: %w", err)
	}
	return nil
}

// readSentinel loads the sentinel if present. Missing file is not an
// error — it just means no update is pending.
func readSentinel(cacheDir string) (*Sentinel, error) {
	data, err := os.ReadFile(sentinelPath(cacheDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Sentinel
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse sentinel: %w", err)
	}
	return &s, nil
}

// removeSentinel clears the sentinel after it has been processed.
// Quiet on missing file.
func removeSentinel(cacheDir string) {
	_ = os.Remove(sentinelPath(cacheDir))
}

// UpdateOutcome describes what happened to a previously-staged update,
// derived by comparing the sentinel against the running binary.
type UpdateOutcome struct {
	Pending      bool      // sentinel exists, install still in flight (helper running)
	Success      bool      // running version matches sentinel ToVersion
	VersionMatch bool      // running version == sentinel ToVersion
	Stale        bool      // sentinel older than 10 minutes — assume helper died
	From         string    // sentinel FromVersion
	To           string    // sentinel ToVersion
	StartedAt    time.Time // sentinel StartedAt
	InstallerLog string    // path to installer log for diagnosis
	HelperLog    string    // path to helper script log for diagnosis
	Reason       string    // human-readable summary
}

// CheckUpdateOutcome inspects the sentinel and the running binary and
// returns a verdict. Callers (tray) use this on startup to log success
// or surface failure — and to clear the sentinel once handled.
//
// runningVersion is the version baked into the current binary. If empty
// (dev build) we treat any sentinel as "can't verify" rather than
// failure, because comparing against "" gives no information.
func (u *Updater) CheckUpdateOutcome(runningVersion string) (*UpdateOutcome, error) {
	s, err := readSentinel(u.cacheDir)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	o := &UpdateOutcome{
		From:         s.FromVersion,
		To:           s.ToVersion,
		StartedAt:    s.StartedAt,
		InstallerLog: s.InstallerLog,
		HelperLog:    s.HelperLog,
	}
	running := normalizeVer(runningVersion)
	expected := normalizeVer(s.ToVersion)

	switch {
	case running == "":
		o.Reason = "dev build — cannot verify update outcome"
	case running == expected:
		o.Success = true
		o.VersionMatch = true
		o.Reason = fmt.Sprintf("update succeeded: %s → %s", s.FromVersion, s.ToVersion)
	case time.Since(s.StartedAt) < 10*time.Minute:
		o.Pending = true
		o.Reason = "update still in progress — helper may be running"
	default:
		o.Stale = true
		o.Reason = fmt.Sprintf("update did not apply: still on %s, expected %s (started %s)", running, expected, s.StartedAt.Format(time.RFC3339))
	}
	return o, nil
}

// ClearOutcome removes the sentinel. Call after CheckUpdateOutcome and
// after the caller has logged / surfaced the result so a stale sentinel
// doesn't keep firing the same notification on every launch.
func (u *Updater) ClearOutcome() { removeSentinel(u.cacheDir) }
