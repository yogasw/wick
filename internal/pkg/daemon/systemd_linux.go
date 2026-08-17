//go:build linux || android

package daemon

import "os"

// systemd_linux.go answers one question honestly: is systemd actually
// running this machine?
//
// It exists because INVOCATION_ID — the variable that used to be trusted
// on its own — is a plain environment variable that other supervisors
// also set. Fly.io's init sets it on a microVM with no systemd installed,
// which made `status` print "running (via systemd)" on a host where
// `systemctl` could not run at all. An operator reads that as evidence
// that scope isolation is available; it is not.

// systemdIsInit reports whether PID 1 is systemd.
//
// This is the same check systemd's own tools make (sd_booted): the
// existence of /run/systemd/system, which the manager creates at boot and
// nothing else does. Reading PID 1's comm as a second signal costs one
// file read and covers a host where the directory is missing but the
// manager is genuinely there.
//
// Deliberately NOT "does the systemctl binary exist": a container can
// carry the binary from its base image while running an entirely
// different init, which is exactly the case this function was written for.
func systemdIsInit() bool {
	// The canonical marker. Present only when the systemd manager booted
	// the system.
	if fi, err := os.Stat("/run/systemd/system"); err == nil && fi.IsDir() {
		return true
	}
	// Fallback: ask PID 1 what it is. On Fly this reads "init"; under
	// systemd it reads "systemd".
	comm, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return false
	}
	return trimNL(string(comm)) == "systemd"
}
