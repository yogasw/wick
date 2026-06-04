//go:build !windows

package safeexec

import (
	"io/fs"
	"os"
	"path/filepath"
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

// findInDir checks for an executable named file directly under dir.
// On unix the executable bit is the only signal — no extension walk.
func findInDir(dir, file string) (string, bool) {
	full := filepath.Join(dir, file)
	if err := findExecutable(full); err == nil {
		return full, true
	}
	return "", false
}
