// Package safeexec provides drop-in replacements for selected os/exec
// helpers whose implementation in Go's standard library uses the
// faccessat2(2) Linux syscall (number 439, introduced in kernel 5.8).
//
// Android's seccomp filter on devices that still ship a 4.x kernel
// (a common combination — Android 13 userland on top of a 4.14
// kernel, for example) rejects faccessat2 with SIGSYS instead of the
// ENOSYS that Go's runtime expects to trigger its faccessat fallback.
// SIGSYS kills the process, so Go's fallback never fires.
//
// The crash signature in the wild looks like:
//
//	SIGSYS: bad system call
//	internal/syscall/unix.faccessat2(...)
//	internal/syscall/unix.Eaccess(...)
//	os/exec.findExecutable(...)
//	os/exec.LookPath(...)
//
// Use LookPath in this package instead of os/exec.LookPath anywhere
// the result will run on Termux/Android phones. The implementation
// here checks executability using os.Stat + the file's mode bits,
// which only goes through the fstatat(2) syscall family that Android
// seccomp does allow.
//
// The .golangci.yml in this repo bans direct calls to exec.LookPath
// via forbidigo. If you have a genuine reason to use the stdlib
// version (e.g. you need euid-based AT_EACCESS semantics in a setuid
// context), add a //nolint:forbidigo comment with a justification.
package safeexec

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// ResolveBin returns the absolute path to name. If name already
// contains a slash it is returned unchanged (caller is assumed to
// have given an absolute or workspace-relative path); otherwise the
// name is resolved via LookPath. Use this before passing name to
// exec.CommandContext to prevent Go's internal LookPath from firing
// during exec.Command construction — that path triggers faccessat2,
// which Android's seccomp on kernel < 5.8 rejects with SIGSYS.
func ResolveBin(name string) (string, error) {
	if hasPathSep(name) {
		return name, nil
	}
	return LookPath(name)
}

// LookPath searches PATH for an executable named file, mirroring
// os/exec.LookPath's semantics but without the faccessat2 syscall.
// If file contains a slash it is checked directly. On Windows, bare
// names without an extension are resolved against PATHEXT so "cmd"
// finds "cmd.exe". Returns an *exec.Error so callers can use
// errors.Is(err, exec.ErrNotFound).
func LookPath(file string) (string, error) {
	if hasPathSep(file) {
		if err := findExecutable(file); err != nil {
			return "", &exec.Error{Name: file, Err: err}
		}
		return file, nil
	}
	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		if full, ok := findInDir(dir, file); ok {
			if !filepath.IsAbs(full) {
				return full, &exec.Error{Name: file, Err: errors.New("resolved to relative path")}
			}
			return full, nil
		}
	}
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

// hasPathSep reports whether s carries a path separator that would
// make exec.Command bypass its internal LookPath. On Windows that's
// either "/" or "\"; on unix only "/" (so backslashes in unix paths
// — rare but legal as a filename — don't accidentally fast-track).
func hasPathSep(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' || s[i] == os.PathSeparator {
			return true
		}
	}
	return false
}
