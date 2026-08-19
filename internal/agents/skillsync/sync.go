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

	"github.com/yogasw/wick/internal/appname"
)

// KnownDirs returns existing skill dirs in a stable order, plus wick's own
// skills dir (~/.<appname>/skills) which is ENSURE-CREATED so the built-in
// wick provider is a first-class skill provider even before any skill is
// added — it appears in the UI chips, sync targets, and the wick session's
// `/` menu without the user having to create the folder first. The other
// provider dirs are only returned when they already exist on disk.
func KnownDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	// Wick's own dir: always present. Defaults to ~/.<app>/skills — the
	// app name comes from appname.Resolve() ("wick" in prod, or the
	// dev/build name like "wick-lab") so dev builds stay isolated — and
	// follows $WICK_DATA_DIR when set. The other entries are third-party
	// provider dirs and stay where those tools put them.
	wickDir := appname.SkillsDir()
	_ = os.MkdirAll(wickDir, 0o755)
	candidates := []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".gemini", "skills"),
		wickDir,
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range candidates {
		if seen[d] {
			continue // guard: appname "agents"/"claude"/… would dup a fixed dir
		}
		seen[d] = true
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
	// e.g. ".claude/skills" → "claude"; ".wick-lab/skills" → "wick-lab"
	seg := filepath.ToSlash(rel)
	if strings.HasPrefix(seg, ".") {
		inner := strings.TrimPrefix(seg, ".")
		if head, _, found := strings.Cut(inner, "/"); found {
			return head
		}
		return inner
	}
	// A dir outside $HOME (e.g. a $WICK_DATA_DIR override): label it by the
	// parent of the skills folder, which is the closest thing to an app name.
	return filepath.Base(filepath.Dir(dir))
}

// OwnLabel is the DirLabel of wick's own skills dir. It equals the resolved app
// name — "wick" for a release build, but "wick-lab" (or whatever wick.yml names)
// for a dev build, since the data dir is ~/.<appname>/.
func OwnLabel() string { return appname.Resolve() }

// DirLabelForProvider maps a PROVIDER TYPE ("claude", "codex", "gemini",
// "wick") to the DirLabel of that provider's skills dir.
//
// They coincide for the CLI providers but NOT for wick: its provider type is
// always "wick" while its dir label follows the app name. Comparing the two
// directly meant a dev build listed zero wick skills — the folder was there,
// the label just never matched — which hid every skill from the `/` composer
// menu and from workflow skill listings.
func DirLabelForProvider(providerType string) string {
	if providerType == "wick" {
		return OwnLabel()
	}
	return providerType
}

// InProvider reports whether a skill is present in the dir belonging to
// providerType. The "agents" dir is shared ground: skills placed there count
// for every provider.
//
// A skill wick ships counts for EVERY provider even though it sits only in
// wick's own dir. It is deliberately never copied elsewhere, so a by-directory
// check would report it missing for claude and codex — and hide from the `/`
// composer a skill their agents can in fact invoke, since each provider trusts
// wick's dir on the argv and is handed the skill's path in its system prompt.
func InProvider(s SkillInfo, providerType string) bool {
	if s.Builtin {
		return true
	}
	want := DirLabelForProvider(providerType)
	for _, loc := range s.InProviders {
		if loc.Label == want || loc.Label == "agents" {
			return true
		}
	}
	return false
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
//
// Copied counts individual FILES written, so one folder skill with a SKILL.md
// plus two reference files reports 3 per destination dir. SkillsCopied counts
// top-level SKILL FOLDERS that gained at least one file — the number a user
// actually cares about, and the one the UI reports. The two are tracked
// separately because the skill dirs also hold loose bookkeeping files
// (CLAUDE.md, README.md, install_skills.sh) that sync but are not skills:
// counting only files made a run that copied zero skills look successful.
type Result struct {
	Copied       int
	SkillsCopied int
	Skipped      int
	Errors       []string
	Dirs         []string
}

// Status returns current skill state without writing anything.
func Status() ([]SkillFile, []string, error) {
	dirs := KnownDirs()
	files := scan(dirs)
	return files, dirs, nil
}

// Sync mirrors every skill to every known dir. Newest mtime wins.
//
// A skill is a FOLDER holding a SKILL.md, so syncing walks each folder and
// resolves the winner PER FILE inside it: dir A can hold a newer SKILL.md
// while dir B holds a newer reference file, and both should survive. Loose
// files sitting in the skills dir (CLAUDE.md, README.md, …) are mirrored too,
// as plain top-level files. Dot entries are provider bookkeeping (.git,
// .DS_Store) and are always skipped.
func Sync() (Result, error) {
	// Refresh the shipped skills first. They live outside this rotation, so
	// this cannot affect what follows — it just means the Sync button also
	// repairs a built-in dir someone deleted or edited.
	if _, err := SyncBuiltin(); err != nil {
		return Result{}, fmt.Errorf("sync builtin skills: %w", err)
	}

	dirs := KnownDirs()
	res := Result{Dirs: dirs}
	if len(dirs) < 2 {
		return res, nil
	}

	// Shipped skills are excluded from the rotation. They live in wick's own
	// dir, which SyncBuiltin rewrites from the binary; a copy in another
	// provider's dir could never be cleaned up the same way, so a skill dropped
	// in a later version would linger there forever and a user edit to the copy
	// would win on mtime and be reverted on the next boot. Providers read them
	// by trusting wick's dir directly instead (see each provider's skilldir.go).
	shipped := BuiltinNames()

	// Winner per RELATIVE path (slash-separated, e.g. "my-skill/SKILL.md" or
	// "README.md"). Keyed on the relative path rather than the top-level entry
	// so a folder is merged file-by-file instead of wholesale.
	type candidate struct {
		srcDir string
		mtime  time.Time
	}
	best := make(map[string]candidate)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if shipped[e.Name()] {
				continue // wick ships it; never mirror it into another provider
			}
			if e.IsDir() {
				for rel, mtime := range walkSkillFolder(dir, e.Name(), &res) {
					if prev, ok := best[rel]; ok && !mtime.After(prev.mtime) {
						continue
					}
					best[rel] = candidate{srcDir: dir, mtime: mtime}
				}
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			rel := e.Name()
			mtime := info.ModTime()
			if prev, ok := best[rel]; ok && !mtime.After(prev.mtime) {
				continue
			}
			best[rel] = candidate{srcDir: dir, mtime: mtime}
		}
	}

	// Track which top-level skills actually gained a file, so the reported
	// count reflects skills rather than the loose files beside them.
	skillsTouched := make(map[string]bool)

	for rel, b := range best {
		for _, dir := range dirs {
			if dir == b.srcDir {
				continue // already the newest copy
			}
			dst := filepath.Join(dir, filepath.FromSlash(rel))
			if fi, err := os.Stat(dst); err == nil && !fi.ModTime().Before(b.mtime) {
				res.Skipped++
				continue
			}
			data, err := os.ReadFile(filepath.Join(b.srcDir, filepath.FromSlash(rel)))
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("read %s/%s: %v", b.srcDir, rel, err))
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dst), err))
				continue
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("write %s/%s: %v", dir, rel, err))
				continue
			}
			_ = os.Chtimes(dst, b.mtime, b.mtime)
			res.Copied++
			if top, _, nested := strings.Cut(rel, "/"); nested {
				skillsTouched[top] = true
			}
		}
	}
	res.SkillsCopied = len(skillsTouched)
	return res, nil
}

// walkSkillFolder returns every file inside <dir>/<name> keyed by its path
// relative to dir (slash-separated), mapped to its mtime. Walk errors are
// recorded rather than dropped: a skill that half-syncs because of a
// permission error must not look like a clean run.
func walkSkillFolder(dir, name string, res *Result) map[string]time.Time {
	out := make(map[string]time.Time)
	root := filepath.Join(dir, name)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("walk %s: %v", path, err))
			return nil // keep going; one bad file shouldn't drop the whole skill
		}
		if d.IsDir() {
			// Skip nested bookkeeping dirs, but never the skill root itself
			// (its name was already vetted by the caller).
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("stat %s: %v", path, ierr))
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = info.ModTime()
		return nil
	})
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("walk %s: %v", root, err))
	}
	return out
}

// SyncEntry mirrors ONE top-level entry (folder skill or loose file) to every
// known dir, resolving the winner per file by mtime — the same rule Sync uses,
// scoped to a single skill.
//
// Picking the source per FILE matters: a folder's own mtime does not change
// when a file inside it is edited, so choosing a source by folder mtime can
// copy a stale SKILL.md over a newer one.
func SyncEntry(name string) (Result, error) {
	dirs := KnownDirs()
	res := Result{Dirs: dirs}
	if name == "" || strings.HasPrefix(name, ".") {
		return res, fmt.Errorf("invalid entry name %q", name)
	}
	// Shipped skills are wick's to place, not the rotation's to spread. See the
	// note in Sync — a copy outside wick's dir can never be cleaned up.
	if IsBuiltinName(name) {
		return res, fmt.Errorf("entry %q ships with wick and is not synced to other providers", name)
	}

	type candidate struct {
		srcDir string
		mtime  time.Time
	}
	best := make(map[string]candidate)
	found := false

	for _, dir := range dirs {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		found = true
		if fi.IsDir() {
			for rel, mtime := range walkSkillFolder(dir, name, &res) {
				if prev, ok := best[rel]; ok && !mtime.After(prev.mtime) {
					continue
				}
				best[rel] = candidate{srcDir: dir, mtime: mtime}
			}
			continue
		}
		if prev, ok := best[name]; ok && !fi.ModTime().After(prev.mtime) {
			continue
		}
		best[name] = candidate{srcDir: dir, mtime: fi.ModTime()}
	}
	if !found {
		return res, fmt.Errorf("entry %q not found in any skill dir", name)
	}

	touched := false
	for rel, b := range best {
		for _, dir := range dirs {
			if dir == b.srcDir {
				continue
			}
			dst := filepath.Join(dir, filepath.FromSlash(rel))
			if fi, err := os.Stat(dst); err == nil && !fi.ModTime().Before(b.mtime) {
				res.Skipped++
				continue
			}
			data, err := os.ReadFile(filepath.Join(b.srcDir, filepath.FromSlash(rel)))
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("read %s/%s: %v", b.srcDir, rel, err))
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dst), err))
				continue
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("write %s/%s: %v", dir, rel, err))
				continue
			}
			_ = os.Chtimes(dst, b.mtime, b.mtime)
			res.Copied++
			touched = true
		}
	}
	if touched {
		res.SkillsCopied = 1
	}
	return res, nil
}

// PushFrom force-copies relPath (a file or folder, possibly nested inside a
// skill) from srcDir to every OTHER known dir, overwriting regardless of mtime.
//
// Unlike Sync/SyncEntry this is deliberately one-directional: the user picked a
// specific provider's copy as the one to propagate, so mtime must not veto it.
func PushFrom(srcDir, relPath string) (Result, error) {
	dirs := KnownDirs()
	res := Result{Dirs: dirs}
	// A shipped skill stays put even under an explicit push: wick rewrites its
	// dir from the binary, so a pushed copy elsewhere would be unmanaged and
	// would outlive the version that shipped it.
	if top, _, _ := strings.Cut(filepath.ToSlash(relPath), "/"); IsBuiltinName(top) {
		return res, fmt.Errorf("entry %q ships with wick and is not pushed to other providers", top)
	}
	src := filepath.Join(srcDir, filepath.FromSlash(relPath))
	fi, err := os.Stat(src)
	if err != nil {
		return res, fmt.Errorf("source %q not found: %w", relPath, err)
	}

	copyOne := func(absSrc string, mtime time.Time, rel string) {
		for _, dst := range dirs {
			if dst == srcDir {
				continue
			}
			dstPath := filepath.Join(dst, filepath.FromSlash(rel))
			data, rerr := os.ReadFile(absSrc)
			if rerr != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("read %s: %v", absSrc, rerr))
				return
			}
			if mkErr := os.MkdirAll(filepath.Dir(dstPath), 0o755); mkErr != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dstPath), mkErr))
				continue
			}
			if wErr := os.WriteFile(dstPath, data, 0o644); wErr != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("write %s: %v", dstPath, wErr))
				continue
			}
			_ = os.Chtimes(dstPath, mtime, mtime)
			res.Copied++
		}
	}

	if !fi.IsDir() {
		copyOne(src, fi.ModTime(), relPath)
		if res.Copied > 0 {
			res.SkillsCopied = 1
		}
		return res, nil
	}

	err = filepath.WalkDir(src, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("walk %s: %v", path, werr))
			return nil
		}
		if d.IsDir() {
			if path != src && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("stat %s: %v", path, ierr))
			return nil
		}
		rel, rerr := filepath.Rel(srcDir, path)
		if rerr != nil {
			return nil
		}
		copyOne(path, info.ModTime(), filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("walk %s: %v", src, err))
	}
	if res.Copied > 0 {
		res.SkillsCopied = 1
	}
	return res, nil
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
