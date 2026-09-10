//go:build linux || android

package daemon

import (
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/safeexec"
)

// supervised_linux.go provides the systemd-user delegation the daemon
// CLI uses when a unit is installed. Termux has no systemd, so these
// short-circuit there and the caller falls back to the PID-file jalur.

// ServiceManaged reports whether a systemd-user unit for appName is
// installed AND enabled — i.e. systemd is the authority for this app's
// lifecycle, so start/stop/restart must delegate to systemctl rather
// than spawn a second PID-file daemon. False on Termux (no systemd) and
// when the unit file is absent.
func ServiceManaged(appName string) bool {
	if isTermux() {
		return false
	}
	target, err := systemdUnitPath(appName)
	if err != nil || !pathExists(target) {
		return false
	}
	// is-enabled exits 0 for enabled/enabled-runtime/static. We only
	// treat an installed+wired unit as "managed"; a leftover disabled
	// file shouldn't hijack the manual jalur.
	out, _ := safeexec.Command("systemctl", "--user", "is-enabled", appName+".service").Output()
	switch strings.TrimSpace(string(out)) {
	case "enabled", "enabled-runtime", "static", "linked", "linked-runtime":
		return true
	}
	return false
}

// ServiceActive reports whether systemctl considers the unit active
// (running). Best-effort; used by `status` for the systemd jalur.
func ServiceActive(appName string) bool {
	out, _ := safeexec.Command("systemctl", "--user", "is-active", appName+".service").Output()
	return strings.TrimSpace(string(out)) == "active"
}

// ServiceCtl runs `systemctl --user <verb> <app>.service` and returns
// the combined error. verb is start | stop | restart. Intentional
// stop/restart here means systemd does NOT treat the exit as a failure,
// so Restart=on-failure won't respawn behind our back.
func ServiceCtl(appName, verb string) error {
	return safeexec.Command("systemctl", "--user", verb, appName+".service").Run()
}

// ServiceMainPID returns the unit's MainPID, or 0 when systemd does not know
// one. Needed by `reload`: a unit written before graceful upgrade existed has
// no ExecReload, so the CLI signals the process itself instead of failing.
func ServiceMainPID(appName string) int {
	out, err := safeexec.Command("systemctl", "--user", "show", "-p", "MainPID", "--value", appName+".service").Output()
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return pid
}
