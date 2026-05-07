//go:build windows && !headless

package systemtray

import "syscall"

// hideConsole detaches the process from its allocated console window so
// double-clicking the binary from Explorer doesn't leave a flashing cmd
// window behind. The binary itself is built as a console subsystem
// executable (no -H=windowsgui) so MCP / CLI subcommands inherit
// stdin/stdout/stderr from their parent pipe — but tray launches need
// no console UI, so we drop it the moment we know we're in tray mode.
//
// Called at the very start of Run(). Safe to call when no console is
// allocated (FreeConsole returns 0 + ERROR_INVALID_PARAMETER, which we
// ignore).
func hideConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")

	// FreeConsole is the cleaner choice: it disconnects the process from
	// the console allocation entirely, so the cmd window vanishes
	// immediately instead of just being hidden. ShowWindow(SW_HIDE) on
	// GetConsoleWindow() would leave the console attached and visible to
	// taskbar / alt-tab.
	kernel32.NewProc("FreeConsole").Call()
}
