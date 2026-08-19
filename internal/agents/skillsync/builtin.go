package skillsync

import (
	"crypto/md5"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

// builtin.go ships wick's own how-to skills inside the binary and extracts them
// into wick's OWN skills dir (~/.<app>/skills), alongside the user's skills.
//
// These are USER-facing docs — how to work with connectors, plugins, workflows,
// and agents — not the development skills that live in the repo's .claude/skills
// (those target someone editing wick itself and reference repo paths that do not
// exist on a user's machine).
//
// Living in the wick dir rather than a private one means every provider that
// already trusts that dir can read them without a second copy on disk. What
// keeps them from leaking into ~/.claude/skills and ~/.codex/skills is
// BuiltinNames: Sync skips those entries, so the shipped copy stays the only
// copy and wick can keep rewriting it safely. Providers reach it by trusting
// the dir directly (see each provider's skilldir.go) instead of owning a fork.

//go:embed all:builtin
var builtinFS embed.FS

// builtinRoot is the embed path prefix. Kept as a constant so the walk and the
// trim agree.
const builtinRoot = "builtin"

// Shipped skills no longer have a dir label of their own: they live in wick's
// own skills dir, so DirLabel reports the app name like any other skill there.
// SkillInfo.Builtin is what tells a shipped skill from a user's, and it comes
// from BuiltinNames rather than from a path.

// builtinWarningMarker appears in every extracted SKILL.md. There is no UI for
// these skills, so this header is the only thing a person who opens the file
// sees telling them their edits will not survive a restart. It is also the
// signal pruning uses to tell a file wick wrote from one a user took over.
const builtinWarningMarker = "MANAGED BY WICK"

// builtinWarningHeader is prepended to each extracted SKILL.md. It sits ABOVE
// the frontmatter as an HTML comment: markdown renderers hide it, and the
// frontmatter parser is unaffected because it only reads the block after the
// first `---`.
const builtinWarningHeader = "<!-- " + builtinWarningMarker + " — DO NOT EDIT.\n" +
	"This file ships inside the wick binary and is rewritten from scratch every\n" +
	"time wick starts or skills are synced. Any change made here is lost.\n" +
	"To change it, edit internal/agents/skillsync/builtin/ in the wick repo. -->\n"

// builtinStampName marks the dir as having been extracted at least once. Dot-
// prefixed so scan() skips it. Its presence is what ReadDirs checks instead of
// the dir itself, which now exists whether or not extraction ever ran.
const builtinStampName = ".wick-builtin"

// BuiltinDir is where the shipped skills live: wick's own skills dir, shared
// with the user's own skills. Kept as a function rather than inlined so the
// several callers that reason about "where do shipped skills live" cannot drift
// apart, and so a future relocation is one edit.
func BuiltinDir() string { return appname.SkillsDir() }

// BuiltinNames returns the top-level entry names that ship inside the binary.
//
// Sync consults this to keep shipped skills OUT of the provider rotation. They
// live in a dir wick rewrites, so a copy in ~/.claude/skills could never be
// cleaned up the same way: a skill dropped in a newer wick version would linger
// there forever, and a user edit to the copy would win on mtime and be silently
// reverted on the next boot. One copy, one owner.
func BuiltinNames() map[string]bool {
	out := map[string]bool{}
	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out[e.Name()] = true
	}
	return out
}

// IsBuiltinName reports whether name is a skill that ships inside the binary.
func IsBuiltinName(name string) bool { return BuiltinNames()[name] }

// ReadDirs returns every directory a skill may be READ from.
//
// The shipped skills now live inside wick's own dir, which KnownDirs already
// ensure-creates and returns, so this is KnownDirs plus a guarantee that the
// shipped copy has actually been extracted. It stays a distinct function
// because callers use it to mean "everything readable", and because extraction
// must not be triggered from the sync path.
func ReadDirs() []string {
	dirs := KnownDirs()
	bd := BuiltinDir()
	if bd == "" {
		return dirs
	}
	// Extract on first use so a read path never depends on boot order having
	// already run SyncBuiltin. Best-effort: a failure just means no shipped
	// skills for this call.
	if _, err := os.Stat(filepath.Join(bd, builtinStampName)); err != nil {
		_, _ = SyncBuiltin()
	}
	if containsDir(dirs, bd) {
		return dirs
	}
	if fi, err := os.Stat(bd); err == nil && fi.IsDir() {
		dirs = append(dirs, bd)
	}
	return dirs
}

// containsDir reports whether dirs holds d.
func containsDir(dirs []string, d string) bool {
	for _, x := range dirs {
		if x == d {
			return true
		}
	}
	return false
}

// SyncBuiltin writes the embedded skills into wick's skills dir.
//
// Content-addressed rather than wipe-and-rewrite: the dir is shared with the
// user's own skills now, so deleting it would destroy their work. Each shipped
// file is compared by MD5 and only rewritten when it actually differs, which
// keeps mtimes stable — Sync resolves winners by mtime, and rewriting an
// unchanged file every boot would make shipped skills perpetually "newest".
//
// Stale shipped skills are still removed, but only ones the embed itself no
// longer contains AND that carry the managed marker, so a user folder that
// happens to share a name is never deleted.
//
// An empty embed is treated as "not built" rather than "no skills": nothing on
// disk is touched and no error is returned, mirroring how a missing embedded
// gate binary degrades instead of destroying state.
func SyncBuiltin() (Result, error) {
	res := Result{}

	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil || len(entries) == 0 {
		// No embedded skills (e.g. a build that trimmed them). Leave whatever
		// is on disk alone — pruning here would delete the previous version's
		// skills and replace them with nothing.
		return res, nil
	}

	dir := BuiltinDir()
	if dir == "" {
		return res, nil
	}
	res.Dirs = []string{dir}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("create builtin skills dir: %w", err)
	}

	shipped := BuiltinNames()
	skills := map[string]bool{}
	// Every path the embed still contains, so the prune below can tell a file
	// wick dropped from one it just wrote.
	live := map[string]bool{}

	err = fs.WalkDir(builtinFS, builtinRoot, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, builtinRoot), "/")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, rerr := builtinFS.ReadFile(p)
		if rerr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("read embedded %s: %v", p, rerr))
			return nil
		}
		if filepath.Base(rel) == "SKILL.md" {
			data = append([]byte(builtinWarningHeader), data...)
		}
		live[filepath.ToSlash(rel)] = true

		// Only rewrite on a real content difference. An unchanged file keeps
		// its mtime, so it never wins Sync's newest-wins race by accident and
		// never looks freshly edited in the UI.
		if fileHasContent(dst, data) {
			res.Skipped++
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dst), err))
			return nil
		}
		if werr := os.WriteFile(dst, data, 0o644); werr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("write %s: %v", dst, werr))
			return nil
		}
		res.Copied++
		if top, _, nested := strings.Cut(rel, "/"); nested {
			skills[top] = true
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("walk embedded skills: %w", err)
	}

	pruneStaleBuiltins(dir, shipped, live, &res)

	if werr := os.WriteFile(filepath.Join(dir, builtinStampName), []byte(builtinReadme), 0o644); werr != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("write stamp: %v", werr))
	}
	res.SkillsCopied = len(skills)
	return res, nil
}

// fileHasContent reports whether the file at p already holds exactly want,
// compared by MD5. A missing or unreadable file reports false so the caller
// simply writes it.
func fileHasContent(p string, want []byte) bool {
	have, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	return md5.Sum(have) == md5.Sum(want)
}

// pruneStaleBuiltins deletes files under shipped skill folders that the embed
// no longer contains, and whole folders wick has stopped shipping.
//
// Two guards keep a user's skill safe. A folder is only removed when the embed
// dropped it AND its SKILL.md still carries the managed marker — a user who
// took a shipped skill over by rewriting that header keeps their copy. Loose
// files inside a still-shipped folder are removed outright: that folder is
// wick's, and a leftover reference file from an older version would otherwise
// be handed to the agent forever.
func pruneStaleBuiltins(dir string, shipped, live map[string]bool, res *Result) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !e.IsDir() {
			continue
		}
		if shipped[name] {
			pruneWithinShippedSkill(dir, name, live, res)
			continue
		}
		if !isManagedSkillFolder(filepath.Join(dir, name)) {
			continue // a user's own skill, or one they took over
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("prune %s: %v", name, err))
		}
	}
}

// pruneWithinShippedSkill removes files inside a still-shipped skill folder
// that the current embed no longer contains.
func pruneWithinShippedSkill(dir, name string, live map[string]bool, res *Result) {
	root := filepath.Join(dir, name)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
		}
		if live[filepath.ToSlash(rel)] {
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("prune %s: %v", rel, rmErr))
		}
		return nil
	})
}

// isManagedSkillFolder reports whether the folder's SKILL.md still carries the
// managed marker, i.e. wick wrote it and the user has not taken it over.
func isManagedSkillFolder(folder string) bool {
	data, err := os.ReadFile(filepath.Join(folder, "SKILL.md"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), builtinWarningMarker)
}

// builtinReadme documents the shipped skills for anyone who finds them on disk.
// Written to the dot-prefixed stamp file so it is never listed as a skill.
const builtinReadme = `# Built-in wick skills — MANAGED BY WICK

Some skills in this directory ship inside the wick binary. Each one carries a
"MANAGED BY WICK" comment at the top of its SKILL.md and is rewritten from the
binary whenever wick starts or skills are synced. Any edit you make to one of
those files is lost.

Your own skills in this directory are untouched — wick only rewrites the files
it ships.

Shipped skills describe how to USE wick: connectors, plugins, workflows, agents.
They stay in this directory only; they are never copied into other providers'
skill directories. Providers read them by trusting this directory directly.

## Want to change a shipped skill?

Edit the source in the wick repo at ` + "`internal/agents/skillsync/builtin/`" + `
and rebuild.
`
