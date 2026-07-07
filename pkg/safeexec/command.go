package safeexec

import (
	"context"
	"os/exec"
	"strings"
)

// Command wraps exec.Command but pre-resolves a bare executable name
// via LookPath so the returned *exec.Cmd carries an absolute path.
// This prevents Go's internal LookPath (which calls faccessat2(2),
// rejected by Android/Termux seccomp on kernel < 5.8 with SIGSYS) from
// firing when the caller later invokes cmd.Start.
//
// Names already containing a path separator (absolute or
// workspace-relative) bypass LookPath inside exec.Command anyway, so
// they are passed through unchanged. Resolution failures are
// swallowed: the returned Cmd keeps the original name, and the error
// surfaces at cmd.Start / cmd.Run — matching upstream semantics.
func Command(name string, args ...string) *exec.Cmd {
	return exec.Command(resolveForExec(name), args...) //nolint:forbidigo // wrapper entrypoint
}

// CommandContext is the context-aware sibling of Command. Same
// pre-resolution semantics.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, resolveForExec(name), args...) //nolint:forbidigo // wrapper entrypoint
}

// resolveForExec returns the absolute path of name when name is a
// bare command, or name itself when it already contains a path
// separator. On lookup error returns name unchanged so the resulting
// *exec.Cmd surfaces the failure at Start/Run time — same shape as
// upstream exec.Command.
func resolveForExec(name string) string {
	if name == "" || strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') {
		return name
	}
	resolved, err := LookPath(name)
	if err != nil {
		return name
	}
	return resolved
}
