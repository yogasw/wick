//go:build linux

package updater

import (
	"fmt"
	"log"
	"os"
	"os/exec"
)

// swapLinuxDeb installs the staged .deb via pkexec dpkg -i in a
// detached helper script. The flow mirrors swapWindows but uses
// pkexec for the privilege prompt (dpkg writes /usr/bin and runs
// postinst, both root-only) and does not need an explicit wait-for-PID
// step — dpkg can rewrite a running ELF on Linux, the running process
// just keeps mapping the inode of the old file until it exits.
//
// The previous flow renamed an extracted ELF onto /usr/bin/<app>:
//
//   - Required write access to /usr/bin (only works if the app was
//     installed user-local, which is not how a .deb installs).
//   - Bypassed dpkg's database, so the next `apt upgrade` would
//     silently re-install whatever the distro mirror shipped.
//   - Skipped any postinst hooks (desktop-database, icon cache,
//     systemd unit reload).
//
// Going through dpkg fixes all three. If pkexec is unavailable
// (headless box, container) the helper falls back to plain `sudo -n`,
// and if that also fails it writes the failure to the helper log so
// the next launch surfaces it via the sentinel.
func swapLinuxDeb(current, staged, cacheDir string, sentinel Sentinel) error {
	helperPath := sentinel.HelperScript
	helperLog := sentinel.HelperLog
	dpkgLog := sentinel.InstallerLog

	if err := writeLinuxHelper(helperPath, helperLog, dpkgLog, current, staged); err != nil {
		return fmt.Errorf("write update helper: %w", err)
	}
	if err := os.Chmod(helperPath, 0o755); err != nil {
		return fmt.Errorf("chmod helper: %w", err)
	}
	pid := os.Getpid()
	log.Printf("updater: scheduling helper=%s pid=%d staged=%s", helperPath, pid, staged)

	// setsid + nohup detaches the helper so it survives our exit.
	// The parent argv[0] is opaque to the helper; we pass our PID
	// explicitly so it can verify-and-wait if needed.
	cmd := exec.Command("setsid", "nohup", helperPath, fmt.Sprintf("%d", pid))
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start update helper: %w", err)
	}
	_ = cmd.Process.Release()
	os.Exit(0)
	return nil
}

// writeLinuxHelper materializes a /bin/sh script that:
//
//  1. Waits a short grace period for the parent to exit.
//  2. Runs `dpkg -i` via pkexec (or sudo -n as fallback).
//  3. Verifies the installed binary exists and is executable.
//  4. Relaunches it.
//  5. Logs everything to helperLog so a failed update can be
//     diagnosed against the sentinel on next start.
//
// The script does NOT delete the staged .deb on failure — the user
// can re-run `sudo dpkg -i` against it manually.
func writeLinuxHelper(helperPath, helperLog, dpkgLog, expectedExe, stagedDeb string) error {
	script := fmt.Sprintf(`#!/bin/sh
set -u
HLOG=%q
DLOG=%q
EXE=%q
DEB=%q

log() { printf '[%%s] %%s\n' "$(date -Is)" "$*" >> "$HLOG"; }

: > "$HLOG"
log "update helper start parent_pid=${1:-?}"

# Best-effort wait for parent — dpkg can replace a running ELF, but
# we still want the old process gone so the relaunch picks up clean.
i=0
while [ "$i" -lt 30 ] && kill -0 "${1:-0}" 2>/dev/null; do
  sleep 1
  i=$((i + 1))
done
log "parent exit grace done after ${i}s"

run_dpkg() {
  if command -v pkexec >/dev/null 2>&1; then
    log "dpkg via pkexec"
    pkexec dpkg -i "$DEB" > "$DLOG" 2>&1
    return $?
  fi
  if command -v sudo >/dev/null 2>&1; then
    log "dpkg via sudo -n"
    sudo -n dpkg -i "$DEB" > "$DLOG" 2>&1
    return $?
  fi
  log "FAIL: neither pkexec nor sudo available"
  return 127
}

run_dpkg
RC=$?
log "dpkg exit=$RC"
if [ "$RC" -ne 0 ]; then
  log "FAIL: dpkg non-zero, see $DLOG"
  exit "$RC"
fi

if [ ! -x "$EXE" ]; then
  log "FAIL: expected exe missing or non-exec: $EXE"
  exit 2
fi

log "OK: launching $EXE"
nohup "$EXE" >/dev/null 2>&1 &
exit 0
`,
		helperLog,
		dpkgLog,
		expectedExe,
		stagedDeb,
	)
	return os.WriteFile(helperPath, []byte(script), 0o755)
}
