//go:build windows

package safeexec

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// findExecutable on Windows: any regular file is considered runnable.
// There's no unix exec bit; the runtime decides via PATHEXT whether a
// given filename can launch as a process. The PATHEXT walk lives in
// findInDir — this function just gates on existence + non-directory.
func findExecutable(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	if d.IsDir() {
		return fs.ErrPermission
	}
	return nil
}

// findInDir resolves file under dir, honoring %PATHEXT% so a bare
// name like "cmd" matches "cmd.exe". If file already carries an
// extension it is tried as-is and not extended — same behavior as
// stdlib exec.LookPath on Windows. Returns the matched full path and
// true, or "" and false when nothing matches.
func findInDir(dir, file string) (string, bool) {
	full := filepath.Join(dir, file)
	if filepath.Ext(file) != "" {
		if err := findExecutable(full); err == nil {
			return full, true
		}
		return "", false
	}
	for _, ext := range pathExt() {
		candidate := full + ext
		if err := findExecutable(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// pathExt returns the lowercased executable extensions from %PATHEXT%,
// falling back to the standard Windows defaults when the env var is
// unset. Lowercasing is purely cosmetic on a case-insensitive FS, but
// keeps test output stable.
func pathExt() []string {
	raw := os.Getenv("PATHEXT")
	if raw == "" {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, e := range parts {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		out = append(out, strings.ToLower(e))
	}
	return out
}
