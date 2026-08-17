//go:build windows

package agents

import "os"

// terminate ends a process on Windows.
//
// Windows has no SIGTERM. os.Process.Kill maps to TerminateProcess, which
// is abrupt by nature — there is no polite equivalent that works for an
// arbitrary process without cooperation from it (a console app would need
// a shared console for CTRL_BREAK, a GUI app a message loop).
//
// So the platforms differ in kind, not just in call: on unix this asks,
// here it ends. Worth knowing before adding a "force" option — that would
// mean something on unix and nothing here.
func terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
