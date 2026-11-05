// Package scriptpath reads a Code Mode script from a file relative to a
// project folder (script_path), with the same confinement for every caller:
// the watch code step, wick_run_code and the run_code workflow node.
package scriptpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes caps a script_path file.
const MaxBytes = 256 << 10

// ErrDenied marks a script_path refused for where it leads (absolute,
// .., or a real path outside the folder). It cannot heal by itself, unlike a
// missing file; errors.Is reports it.
var ErrDenied = errors.New("script_path denied")

type denied struct{ msg string }

func (e denied) Error() string        { return e.msg }
func (e denied) Is(target error) bool { return target == ErrDenied }

// Valid is the shape check of a script_path; where the path really
// leads is checked again on every read (Read).
func Valid(p string) error {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return errors.New("must be relative to the project folder")
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return errors.New("must stay inside the project folder (no ..)")
		}
	}
	return nil
}

// Read loads script_path rel from dir, fresh on every call. The real
// path (symlinks resolved) must sit inside dir's real path; anything else is
// refused with ErrDenied.
func Read(dir, rel string) (string, error) {
	if dir == "" {
		return "", errors.New("no project folder to read script_path from")
	}
	if err := Valid(rel); err != nil {
		return "", denied{"script_path " + rel + ": " + err.Error()}
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("project folder: %w", err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("script_path %s: file not found in the project folder", rel)
		}
		return "", fmt.Errorf("script_path %s: %w", rel, err)
	}
	if !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return "", denied{"script_path " + rel + " resolves outside the project folder"}
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("script_path %s: %w", rel, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("script_path %s: not a regular file", rel)
	}
	if fi.Size() > MaxBytes {
		return "", fmt.Errorf("script_path %s: too large (%d bytes, max %d)", rel, fi.Size(), MaxBytes)
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return "", fmt.Errorf("script_path %s: %w", rel, err)
	}
	return string(b), nil
}
