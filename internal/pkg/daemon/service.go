package daemon

import (
	"errors"
	"os"

	"github.com/yogasw/wick/internal/autostart"
	"github.com/yogasw/wick/internal/pkg/env"
	"github.com/yogasw/wick/internal/userconfig"
)

// ServiceState describes whether per-OS auto-start integration is
// installed and (where the platform exposes it) whether the service
// is currently active.
type ServiceState struct {
	Installed bool   // unit / script / login-item is registered with the OS
	Active    bool   // best-effort runtime check; not always supported
	Path      string // canonical location of the registration
	Backend   string // "autostart-gui", "systemd-user", "termux-boot", "headless-unsupported"
	Note      string // free-form hint shown in `service status`
}

// ErrNotInstalled signals UninstallService was called when no service
// integration was found.
var ErrNotInstalled = errors.New("service not installed")

// InstallService registers the binary to launch at user login / boot.
// Routing depends on the runtime environment:
//
//	GUI present (Win / Mac / desktop Linux) → forwards to the same
//	internal/autostart entry the tray uses AND flips
//	userconfig.AutoStartApp=true so the tray's `Auto-start app at
//	login` checkbox stays in sync with what the CLI did.
//
//	Headless (Termux, Linux server / RPi) → writes a daemon-style
//	auto-start unit (systemd-user or Termux:Boot script) that runs
//	`<exe> all` directly.
//
// Re-running install over an existing install is safe — the GUI path
// rewrites the autostart entry, the headless path rewrites the unit.
func InstallService(p Paths, appName string) error {
	if env.HasGUI() {
		if err := autostart.Enable(appName); err != nil {
			return err
		}
		return setAutoStartApp(appName, true)
	}
	return installHeadless(p, appName)
}

// UninstallService removes whichever auto-start mechanism is in
// place. ErrNotInstalled is returned if nothing was registered. On
// the GUI path it also flips userconfig.AutoStartApp=false so the
// tray's checkbox follows the CLI.
func UninstallService(p Paths, appName string) error {
	if env.HasGUI() {
		if !autostart.IsEnabled(appName) {
			// Keep userconfig in sync even when OS entry was already
			// gone — covers the "I ticked it off in tray, then ran
			// CLI uninstall" case.
			_ = setAutoStartApp(appName, false)
			return ErrNotInstalled
		}
		if err := autostart.Disable(appName); err != nil {
			return err
		}
		return setAutoStartApp(appName, false)
	}
	return uninstallHeadless(appName)
}

// ServiceStatus reports the registration state for whichever backend
// applies to the current host. On GUI hosts Installed/Active reflects
// the actual OS entry (what fires at login); a separate note flags
// when userconfig.AutoStartApp disagrees so the user can spot drift.
func ServiceStatus(p Paths, appName string) (ServiceState, error) {
	if env.HasGUI() {
		st := ServiceState{
			Backend: "autostart-gui",
			Path:    autostart.Path(appName),
			Note:    "managed by the tray — toggle `Auto-start app at login` in tray preferences for an interactive view",
		}
		st.Installed = autostart.IsEnabled(appName)
		// Active mirrors Installed for the GUI path — the autostart
		// entry only fires at login, no live "is it running" probe.
		st.Active = st.Installed
		if cfg, err := userconfig.Load(appName); err == nil && cfg.AutoStartApp != st.Installed {
			st.Note = "config / OS state drift — userconfig AutoStartApp=" +
				boolWord(cfg.AutoStartApp) + " but OS entry=" + boolWord(st.Installed) +
				". Re-run `service install` or `service uninstall` to re-sync."
		}
		return st, nil
	}
	return statusHeadless(appName)
}

// setAutoStartApp mirrors the OS-level install/uninstall back into
// userconfig.AutoStartApp so the tray's checkbox renders the same
// state the user just chose from the CLI. Soft-fails on read errors
// (a missing config means the tray will write a default next launch).
func setAutoStartApp(appName string, on bool) error {
	cfg, err := userconfig.Load(appName)
	if err != nil {
		return nil //nolint:nilerr // soft-fail — see godoc
	}
	if cfg.AutoStartApp == on {
		return nil
	}
	cfg.AutoStartApp = on
	return userconfig.Save(appName, cfg)
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// pathExists is a tiny helper used across the platform files. Lives
// here so each per-OS file doesn't redeclare it.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
