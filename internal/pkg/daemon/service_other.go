//go:build !linux && !android

package daemon

import "errors"

// On non-Linux platforms there is no headless backend — Windows /
// macOS users go through the GUI autostart path in service.go
// (internal/autostart). These stubs only fire when env.HasGUI
// returned false on a non-Linux build, which in practice means a
// SSH session into a Mac with no Aqua: the right answer is the
// foreground subcommands, not a fragile alternate scheduler.

var errHeadlessUnsupported = errors.New(
	"headless service install is only available on Linux / Termux — " +
		"on this OS use the tray's `Auto-start app at login` toggle, " +
		"or run `<app> all` manually under your own supervisor")

func installHeadless(p Paths, appName string) error   { return errHeadlessUnsupported }
func uninstallHeadless(appName string) error          { return errHeadlessUnsupported }
func statusHeadless(appName string) (ServiceState, error) {
	return ServiceState{
		Backend: "headless-unsupported",
		Note:    errHeadlessUnsupported.Error(),
	}, nil
}
