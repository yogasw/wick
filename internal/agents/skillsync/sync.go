// Package skillsync mirrors skill files across all agent skill directories
// (~/.claude/skills, ~/.codex/skills, ~/.gemini/skills, ~/.agents/skills)
// without symlinks. Any file in any dir is copied to all others.
// Newest mtime wins on conflict so no work is lost.
package skillsync

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// KnownDirs returns existing skill dirs in a stable order.
func KnownDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".gemini", "skills"),
	}
	var out []string
	for _, d := range candidates {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// DirLabel returns a short human label for a dir path (e.g. "claude", "codex").
func DirLabel(dir string) string {
	home, _ := os.UserHomeDir()
	rel, err := filepath.Rel(home, dir)
	if err != nil {
		return filepath.Base(dir)
	}
	// e.g. ".claude/skills" → "claude"
	parts := filepath.SplitList(rel)
	_ = parts
	// manual parse: rel = ".claude/skills" → [".claude","skills"]
	seg := filepath.ToSlash(rel)
	switch {
	case len(seg) > 0 && seg[0] == '.':
		// strip leading dot and trailing /skills
		inner := seg[1:]
		if i := indexOf(inner, '/'); i >= 0 {
			return inner[:i]
		}
		return inner
	default:
		return filepath.Base(filepath.Dir(dir))
	}
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// SkillEntry represents one top-level entry (file or folder) found across skill dirs.
type SkillEntry struct {
	Name    string    // entry name (folder or filename)
	IsDir   bool      // true if it's a folder in at least one dir
	Sources []string  // dirs where this entry exists
	Missing []string  // dirs where this entry is absent
	Newest  time.Time // mtime of newest copy
}

// SkillFile is an alias kept for callers that only care about files.
type SkillFile = SkillEntry

// Result is returned by Sync and Upload.
type Result struct {
	Copied  int
	Skipped int
	Errors  []string
	Dirs    []string
}

// Status returns current skill state without writing anything.
func Status() ([]SkillFile, []string, error) {
	dirs := KnownDirs()
	files := scan(dirs)
	return files, dirs, nil
}

// Sync copies every skill file to every known dir. Newest mtime wins.
func Sync() (Result, error) {
	dirs := KnownDirs()
	res := Result{Dirs: dirs}
	if len(dirs) < 2 {
		return res, nil
	}

	type entry struct {
		content []byte
		mtime   time.Time
		srcDir  string
	}
	best := make(map[string]entry)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			name := e.Name()
			mtime := info.ModTime()
			if prev, ok := best[name]; ok && !mtime.After(prev.mtime) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("read %s/%s: %v", dir, name, err))
				continue
			}
			best[name] = entry{content: data, mtime: mtime, srcDir: dir}
		}
	}

	for name, b := range best {
		for _, dir := range dirs {
			dst := filepath.Join(dir, name)
			if fi, err := os.Stat(dst); err == nil && !fi.ModTime().Before(b.mtime) {
				res.Skipped++
				continue
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", dir, err))
				continue
			}
			if err := os.WriteFile(dst, b.content, 0o644); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("write %s/%s: %v", dir, name, err))
				continue
			}
			_ = os.Chtimes(dst, b.mtime, b.mtime)
			res.Copied++
		}
	}
	return res, nil
}

// Upload writes content as filename into all known dirs immediately.
// Used when the user uploads a new skill file from the UI.
func Upload(filename string, content []byte) (Result, error) {
	dirs := KnownDirs()
	res := Result{Dirs: dirs}
	now := time.Now()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", dir, err))
			continue
		}
		dst := filepath.Join(dir, filename)
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("write %s: %v", dst, err))
			continue
		}
		_ = os.Chtimes(dst, now, now)
		res.Copied++
	}
	return res, nil
}

// DeleteFromAll removes a skill file from all known dirs.
func DeleteFromAll(filename string) (int, error) {
	dirs := KnownDirs()
	removed := 0
	for _, dir := range dirs {
		dst := filepath.Join(dir, filename)
		if err := os.Remove(dst); err == nil {
			removed++
		}
	}
	return removed, nil
}

// ReadFile returns the content of a skill file from the first dir that has it.
func ReadFile(filename string) ([]byte, string, error) {
	dirs := KnownDirs()
	for _, dir := range dirs {
		path := filepath.Join(dir, filename)
		data, err := os.ReadFile(path)
		if err == nil {
			return data, path, nil
		}
	}
	return nil, "", fmt.Errorf("skill file %q not found in any skill dir", filename)
}

func scan(dirs []string) []SkillEntry {
	type meta struct {
		mtime time.Time
		isDir bool
		dir   string
	}
	byName := make(map[string][]meta)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			byName[e.Name()] = append(byName[e.Name()], meta{
				mtime: info.ModTime(),
				isDir: e.IsDir(),
				dir:   dir,
			})
		}
	}

	var out []SkillEntry
	for name, metas := range byName {
		se := SkillEntry{Name: name}
		srcDirs := make(map[string]bool)
		for _, m := range metas {
			srcDirs[m.dir] = true
			if m.isDir {
				se.IsDir = true
			}
			if m.mtime.After(se.Newest) {
				se.Newest = m.mtime
			}
		}
		for _, d := range dirs {
			if srcDirs[d] {
				se.Sources = append(se.Sources, d)
			} else {
				se.Missing = append(se.Missing, d)
			}
		}
		out = append(out, se)
	}
	sort.Slice(out, func(i, j int) bool {
		// folders first, then alpha
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ListDir returns entries inside a specific subfolder across all skill dirs.
// entryName is a top-level folder name (e.g. "imagegen").
// Returns entries found in any dir, deduped by name, isDir tracked.
// Second return is the list of dirs where entryName exists.
func ListDir(entryName string) ([]SkillEntry, []string, error) {
	dirs := KnownDirs()
	type meta struct {
		mtime time.Time
		isDir bool
		dir   string
	}
	byName := make(map[string][]meta)
	var presentIn []string

	for _, dir := range dirs {
		sub := filepath.Join(dir, entryName)
		fi, err := os.Stat(sub)
		if err != nil || !fi.IsDir() {
			continue
		}
		presentIn = append(presentIn, dir)
		entries, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			byName[e.Name()] = append(byName[e.Name()], meta{
				mtime: info.ModTime(),
				isDir: e.IsDir(),
				dir:   dir,
			})
		}
	}

	var out []SkillEntry
	for name, metas := range byName {
		se := SkillEntry{Name: name}
		srcDirs := make(map[string]bool)
		for _, m := range metas {
			srcDirs[m.dir] = true
			if m.isDir {
				se.IsDir = true
			}
			if m.mtime.After(se.Newest) {
				se.Newest = m.mtime
			}
		}
		for _, d := range presentIn {
			if srcDirs[d] {
				se.Sources = append(se.Sources, d)
			} else {
				se.Missing = append(se.Missing, d)
			}
		}
		out = append(out, se)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, presentIn, nil
}

// ZipEntry creates a zip archive of entryName (folder or file) from first dir that has it.
// Returns zip bytes.
func ZipEntry(entryName string) ([]byte, error) {
	dirs := KnownDirs()
	var srcDir string
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, entryName)); err == nil {
			srcDir = dir
			break
		}
	}
	if srcDir == "" {
		return nil, fmt.Errorf("entry %q not found in any skill dir", entryName)
	}

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	base := filepath.Join(srcDir, entryName)
	fi, err := os.Stat(base)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		err = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(srcDir, path)
			rel = filepath.ToSlash(rel)
			if info.IsDir() {
				_, werr := zw.Create(rel + "/")
				return werr
			}
			w, werr := zw.Create(rel)
			if werr != nil {
				return werr
			}
			f, ferr := os.Open(path)
			if ferr != nil {
				return ferr
			}
			defer f.Close()
			_, cerr := io.Copy(w, f)
			return cerr
		})
	} else {
		w, werr := zw.Create(entryName)
		if werr != nil {
			return nil, werr
		}
		data, rerr := os.ReadFile(base)
		if rerr != nil {
			return nil, rerr
		}
		_, err = w.Write(data)
	}
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DeleteEntry removes entryName (file or folder recursively) from all dirs.
func DeleteEntry(name string) (int, error) {
	dirs := KnownDirs()
	removed := 0
	for _, dir := range dirs {
		dst := filepath.Join(dir, name)
		if err := os.RemoveAll(dst); err == nil {
			if _, serr := os.Stat(dst); os.IsNotExist(serr) {
				removed++
			}
		}
	}
	return removed, nil
}

// DeleteEntryFromDir removes entryName only from one specific dir.
func DeleteEntryFromDir(dir, name string) error {
	dst := filepath.Join(dir, name)
	return os.RemoveAll(dst)
}

// UploadProcessed handles upload logic:
//   - .md / .txt → write to <skillDir>/<stem>/SKILL<ext>
//   - .zip / .skills → extract zip with root-folder detection
//
// Returns (folderName, Result, error).
func UploadProcessed(filename string, data []byte) (string, Result, error) {
	dirs := uploadDirs()
	res := Result{Dirs: dirs}
	ext := strings.ToLower(filepath.Ext(filename))
	stem := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))

	switch ext {
	case ".md", ".txt":
		skillFilename := "SKILL" + ext
		folderName := stem
		for _, dir := range dirs {
			dest := filepath.Join(dir, folderName)
			if err := os.MkdirAll(dest, 0o755); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", dest, err))
				continue
			}
			dst := filepath.Join(dest, skillFilename)
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("write %s: %v", dst, err))
				continue
			}
			res.Copied++
		}
		return folderName, res, nil

	case ".zip", ".skills":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", res, fmt.Errorf("invalid zip: %w", err)
		}
		folderName, plan, err := planZipExtraction(stem, zr.File)
		if err != nil {
			return "", res, err
		}
		for _, dir := range dirs {
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", dir, mkErr))
				continue
			}
			for _, p := range plan {
				dst := filepath.Join(dir, filepath.FromSlash(p.dest))
				if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dst), mkErr))
					continue
				}
				rc, oErr := p.f.Open()
				if oErr != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("open zip entry %s: %v", p.f.Name, oErr))
					continue
				}
				fdata, rErr := io.ReadAll(rc)
				rc.Close()
				if rErr != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("read zip entry %s: %v", p.f.Name, rErr))
					continue
				}
				if wErr := os.WriteFile(dst, fdata, 0o644); wErr != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("write %s: %v", dst, wErr))
					continue
				}
				res.Copied++
			}
		}
		return folderName, res, nil

	default:
		return "", res, fmt.Errorf("unsupported file type: %s", ext)
	}
}

func uploadDirs() []string {
	if dirs := KnownDirs(); len(dirs) > 0 {
		return dirs
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".claude", "skills")}
}

type zipEntryPlan struct {
	f    *zip.File
	dest string
}

var skillMetadataNames = []string{"skill.md", "skill.txt", "tool.md", "tool.txt", "readme.md"}

func isJunkPath(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		switch seg {
		case "__MACOSX", ".DS_Store", "Thumbs.db", "desktop.ini":
			return true
		}
		if strings.HasPrefix(seg, "._") {
			return true
		}
	}
	return false
}

func planZipExtraction(stem string, files []*zip.File) (string, []zipEntryPlan, error) {
	type realFile struct {
		f    *zip.File
		name string
	}
	var reals []realFile
	for _, f := range files {
		name := strings.TrimPrefix(filepath.ToSlash(f.Name), "./")
		if name == "" {
			continue
		}
		if strings.Contains(name, "..") {
			return "", nil, fmt.Errorf("zip contains unsafe path: %s", f.Name)
		}
		if f.FileInfo().IsDir() || isJunkPath(name) {
			continue
		}
		reals = append(reals, realFile{f: f, name: name})
	}
	if len(reals) == 0 {
		return "", nil, fmt.Errorf("archive has no usable skill files")
	}

	prefix := ""
	anchored := false
	for _, meta := range skillMetadataNames {
		best := ""
		bestDepth := -1
		for _, r := range reals {
			if strings.ToLower(path.Base(r.name)) != meta {
				continue
			}
			if depth := strings.Count(r.name, "/"); bestDepth == -1 || depth < bestDepth {
				best = r.name
				bestDepth = depth
			}
		}
		if bestDepth != -1 {
			if i := strings.LastIndex(best, "/"); i >= 0 {
				prefix = best[:i+1]
			}
			anchored = true
			break
		}
	}

	if anchored {
		folderName := stem
		if prefix != "" {
			folderName = path.Base(strings.TrimSuffix(prefix, "/"))
		}
		var plan []zipEntryPlan
		for _, r := range reals {
			if prefix != "" && !strings.HasPrefix(r.name, prefix) {
				continue
			}
			plan = append(plan, zipEntryPlan{f: r.f, dest: folderName + "/" + strings.TrimPrefix(r.name, prefix)})
		}
		if len(plan) == 0 {
			return "", nil, fmt.Errorf("archive has no usable skill files")
		}
		return folderName, plan, nil
	}

	roots := map[string]bool{}
	for _, r := range reals {
		top := r.name
		if i := strings.Index(top, "/"); i >= 0 {
			top = top[:i]
		}
		roots[top] = true
	}
	if len(roots) == 1 {
		var only string
		for k := range roots {
			only = k
		}
		allUnder := true
		for _, r := range reals {
			if !strings.HasPrefix(r.name, only+"/") {
				allUnder = false
				break
			}
		}
		if allUnder {
			var plan []zipEntryPlan
			for _, r := range reals {
				plan = append(plan, zipEntryPlan{f: r.f, dest: r.name})
			}
			return only, plan, nil
		}
	}
	var plan []zipEntryPlan
	for _, r := range reals {
		plan = append(plan, zipEntryPlan{f: r.f, dest: stem + "/" + r.name})
	}
	return stem, plan, nil
}
