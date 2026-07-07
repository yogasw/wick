//go:build windows

package safeexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// fixBatchQuoting works around a Go os/exec bug on Windows when the
// executable is a .bat or .cmd file (golang/go#68313, and the earlier
// CVE-2024-24576 hardening that made it stricter).
//
// Go runs a batch file by invoking it through cmd.exe, but cmd.exe
// re-parses the command line with its OWN quoting rules — which differ
// from the C-runtime rules Go's default argv->CmdLine builder uses. The
// symptom in the wild: an npm-installed CLI shim at
// "C:\Program Files\nodejs\codex.cmd" launched with ANY argument that
// contains a space fails with
//
//	'C:\Program' is not recognized as an internal or external command
//
// because cmd.exe splits the executable path at its embedded space. It
// only misfires when at least one argument itself needs quoting — a
// space-free argv (e.g. "--version") happens to survive.
//
// The fix is to build the command line ourselves with cmd.exe-correct
// quoting and hand it to the child via SysProcAttr.CmdLine, which tells
// Go to skip its own quoting. We wrap each token in double quotes and
// caret-escape the cmd.exe metacharacters, matching what a robust
// %*-forwarding batch shim expects.
//
// Only .bat/.cmd targets are touched; .exe launches use Go's normal
// path (no cmd.exe in the middle, so no re-parse to defend against).
//
// The fix launches cmd.exe ourselves and builds the command line in the
// exact form cmd.exe /c wants: `cmd /s /c "<batch> <args...>"`. The
// OUTER pair of quotes around the whole inner command is the key —
// with /s, cmd.exe strips the first and last quote of the argument and
// runs the remainder verbatim, so a quoted batch path with an embedded
// space survives intact. Without the outer pair, cmd.exe treats the
// inner text as multiple tokens and splits the path at its space, hence
// "'C:\Program' is not recognized".
func fixBatchQuoting(cmd *exec.Cmd) {
	if cmd == nil || cmd.Path == "" {
		return
	}
	lower := strings.ToLower(cmd.Path)
	if !strings.HasSuffix(lower, ".bat") && !strings.HasSuffix(lower, ".cmd") {
		return
	}

	// Inner command: quoted batch path followed by each quoted argument.
	// cmd.Args[0] is the program name (== cmd.Path); the rest are real args.
	inner := make([]string, 0, len(cmd.Args))
	inner = append(inner, quoteCmdExeArg(cmd.Path))
	for _, a := range cmd.Args[1:] {
		inner = append(inner, quoteCmdExeArg(a))
	}
	innerLine := strings.Join(inner, " ")

	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}

	// Redirect the actual launch to cmd.exe. Go uses SysProcAttr.CmdLine
	// verbatim as the child's command line, so cmd.Path must be the
	// executable that line invokes.
	cmd.Path = comspec
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// /s + outer quotes => cmd.exe strips exactly the first and last quote.
	cmd.SysProcAttr.CmdLine = `"` + comspec + `" /s /c "` + innerLine + `"`
}

// quoteCmdExeArg wraps a single token in double quotes so an embedded
// space survives cmd.exe's re-tokenization. Embedded double quotes are
// doubled ("") — the convention the CRT argv parser accepts inside a
// quoted run. cmd.exe metacharacters ( & | < > ^ ) do not need caret
// escaping here because they sit inside the double-quoted inner command
// that /s passes through verbatim.
func quoteCmdExeArg(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
