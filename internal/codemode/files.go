package codemode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// wick.file: a script reads and writes files ONLY inside the codemode/
// folder of its project folder, so an agent and its scripts can hand each
// other JSON/text. The runner never touches the disk: every op comes back as
// a "file" event and is done here, with these checks on every op.
const (
	// FilesDir is the one folder of the project a script may touch.
	FilesDir = "codemode"
	// MaxFileBytes caps one read or write.
	MaxFileBytes = 1 << 20
	// MaxFolderBytes and MaxFiles cap the whole codemode/ folder; a write
	// that would pass either is refused.
	MaxFolderBytes = 20 << 20
	MaxFiles       = 1000
)

// FeatureFile is announced by both sides (run request, done event) when
// they speak the "file" event.
const FeatureFile = "file"

// ErrFileDenied marks a path refused for where it leads (absolute, ..,
// a symlink out of codemode/); errors.Is reports it.
var ErrFileDenied = errors.New("wick.file path denied")

type fileDenied struct{ msg string }

func (e fileDenied) Error() string        { return e.msg }
func (e fileDenied) Is(target error) bool { return target == ErrFileDenied }

// FileEntry is one item of wick.file.list.
type FileEntry struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// FileOp runs one wick.file op for the project folder dir and returns its
// JSON result: read → string, write → {path, bytes}, list → [FileEntry],
// exists → bool.
func FileOp(dir, op, rel, data string) (json.RawMessage, error) {
	if dir == "" {
		return nil, errors.New("wick.file needs a project folder and this run has none")
	}
	base, err := filesBase(dir)
	if err != nil {
		return nil, err
	}
	var v any
	switch op {
	case "read":
		v, err = fileRead(base, rel)
	case "write":
		v, err = fileWrite(base, rel, data)
	case "list":
		v, err = fileList(base, rel)
	case "exists":
		v, err = fileExists(base, rel)
	default:
		return nil, fmt.Errorf("wick.file: unknown op %q", op)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// filesBase is the real path of <dir>/codemode (which may not exist yet).
// codemode/ itself must not be a symlink.
func filesBase(dir string) (string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("project folder: %w", err)
	}
	base := filepath.Join(root, FilesDir)
	fi, err := os.Lstat(base)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return base, nil
	case err != nil:
		return "", err
	case fi.Mode()&os.ModeSymlink != 0:
		return "", fileDenied{FilesDir + "/ is a symlink; wick.file only uses a real folder"}
	case !fi.IsDir():
		return "", errors.New(FilesDir + "/ in the project folder is not a folder")
	}
	return base, nil
}

// cleanRel checks the shape of a path relative to codemode/.
func cleanRel(rel string, allowRoot bool) (string, error) {
	rel = strings.TrimSpace(rel)
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "~") {
		return "", fileDenied{"wick.file " + rel + ": paths are relative to " + FilesDir + "/"}
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return "", fileDenied{"wick.file " + rel + ": must stay inside " + FilesDir + "/ (no ..)"}
		}
	}
	rel = filepath.Clean(rel)
	if rel == "." && !allowRoot {
		return "", errors.New("wick.file: a file path is required")
	}
	return rel, nil
}

// inside resolves p (symlinks followed) and checks it sits inside base.
func inside(base, p, rel string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	if real != base && !strings.HasPrefix(real, base+string(os.PathSeparator)) {
		return "", fileDenied{"wick.file " + rel + ": resolves outside " + FilesDir + "/"}
	}
	return real, nil
}

func fileRead(base, rel string) (string, error) {
	rel, err := cleanRel(rel, false)
	if err != nil {
		return "", err
	}
	real, err := inside(base, filepath.Join(base, rel), rel)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("wick.file %s: not found in %s/", rel, FilesDir)
	}
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("wick.file %s: not a regular file", rel)
	}
	if fi.Size() > MaxFileBytes {
		return "", fmt.Errorf("wick.file %s: too large (%d bytes, max %d)", rel, fi.Size(), MaxFileBytes)
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func fileWrite(base, rel, data string) (map[string]any, error) {
	rel, err := cleanRel(rel, false)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("wick.file %s: too large (%d bytes, max %d)", rel, len(data), MaxFileBytes)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	// Create the parent one level at a time, checking each existing level
	// first: MkdirAll would follow a symlinked level out of codemode/.
	parent := base
	if d := filepath.Dir(rel); d != "." {
		for _, part := range strings.Split(d, string(os.PathSeparator)) {
			next := filepath.Join(parent, part)
			if _, err := os.Lstat(next); errors.Is(err, os.ErrNotExist) {
				if err := os.Mkdir(next, 0o755); err != nil {
					return nil, err
				}
			}
			if parent, err = inside(base, next, rel); err != nil {
				return nil, err
			}
		}
	}
	target := filepath.Join(parent, filepath.Base(rel))
	var oldSize int64
	isNew := true
	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			real, err := inside(base, target, rel)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if err == nil {
				fi, _ = os.Stat(real)
			}
		}
		if fi != nil && fi.Mode().IsRegular() {
			oldSize, isNew = fi.Size(), false
		} else if fi != nil && fi.IsDir() {
			return nil, fmt.Errorf("wick.file %s: is a folder", rel)
		}
	}
	size, count, err := folderUsage(base)
	if err != nil {
		return nil, err
	}
	if isNew && count+1 > MaxFiles {
		return nil, fmt.Errorf("wick.file %s: %s/ already holds %d files (max %d)", rel, FilesDir, count, MaxFiles)
	}
	if size-oldSize+int64(len(data)) > MaxFolderBytes {
		return nil, fmt.Errorf("wick.file %s: %s/ would pass %d bytes (max %d); delete old files first", rel, FilesDir, size-oldSize+int64(len(data)), MaxFolderBytes)
	}
	// Temp file + rename: a reader never sees half a file, and a symlink at
	// target is replaced, never written through.
	tmp, err := os.CreateTemp(parent, ".wick-file-*")
	if err != nil {
		return nil, err
	}
	if _, err := tmp.WriteString(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return map[string]any{"path": filepath.ToSlash(rel), "bytes": len(data)}, nil
}

func fileList(base, rel string) ([]FileEntry, error) {
	rel, err := cleanRel(rel, true)
	if err != nil {
		return nil, err
	}
	out := []FileEntry{}
	start, err := inside(base, filepath.Join(base, rel), rel)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	// WalkDir does not follow symlinks, so nothing outside is listed.
	err = filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if len(out) >= MaxFiles {
			return filepath.SkipAll
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		r, _ := filepath.Rel(base, p)
		out = append(out, FileEntry{Path: filepath.ToSlash(r), Size: fi.Size(), Modified: fi.ModTime().UTC().Format(time.RFC3339)})
		return nil
	})
	return out, err
}

func fileExists(base, rel string) (bool, error) {
	rel, err := cleanRel(rel, false)
	if err != nil {
		return false, err
	}
	_, err = inside(base, filepath.Join(base, rel), rel)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// folderUsage is the bytes and regular files under base.
func folderUsage(base string) (size int64, count int, err error) {
	err = filepath.WalkDir(base, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if fi, ierr := d.Info(); ierr == nil {
			size += fi.Size()
			count++
		}
		return nil
	})
	return size, count, err
}
