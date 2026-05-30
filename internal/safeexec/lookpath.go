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
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// findExecutable reports whether file is a regular file with at
// least one executable mode bit set. Unlike os/exec.findExecutable
// it never calls unix.Eaccess (and therefore never reaches
// faccessat2). This sacrifices the euid-based AT_EACCESS check —
// fine for wick because the process is never setuid; effective
// and real uid are always the same.
func findExecutable(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	if m := d.Mode(); !m.IsDir() && m&0o111 != 0 {
		return nil
	}
	if d.IsDir() {
		return syscall.EISDIR
	}
	return fs.ErrPermission
}

// ResolveBin returns the absolute path to name. If name already
// contains a slash it is returned unchanged (caller is assumed to
// have given an absolute or workspace-relative path); otherwise the
// name is resolved via LookPath. Use this before passing name to
// exec.CommandContext to prevent Go's internal LookPath from firing
// during exec.Command construction — that path triggers faccessat2,
// which Android's seccomp on kernel < 5.8 rejects with SIGSYS.
func ResolveBin(name string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	return LookPath(name)
}

// LookPath searches PATH for an executable named file, mirroring
// os/exec.LookPath's semantics but without the faccessat2 syscall.
// If file contains a slash it is checked directly. Returns an
// *exec.Error so callers can use errors.Is(err, exec.ErrNotFound).
func LookPath(file string) (string, error) {
	if strings.Contains(file, "/") {
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
		full := filepath.Join(dir, file)
		if err := findExecutable(full); err == nil {
			if !filepath.IsAbs(full) {
				return full, &exec.Error{Name: file, Err: errors.New("resolved to relative path")}
			}
			return full, nil
		}
	}
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}
