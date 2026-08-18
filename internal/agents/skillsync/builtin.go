package skillsync

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

// builtin.go ships wick's own how-to skills inside the binary and extracts them
// to a directory wick alone owns.
//
// These are USER-facing docs — how to work with connectors, plugins, workflows,
// and agents — not the development skills that live in the repo's .claude/skills
// (those target someone editing wick itself and reference repo paths that do not
// exist on a user's machine).
//
// The dir is deliberately OUTSIDE the sync rotation (see KnownDirs): it is not a
// provider dir, so Sync never reads or writes it. That separation is what makes
// "read-only" structurally true rather than a rule someone has to remember —
// user skills cannot be overwritten by a shipped one, and a shipped one cannot
// be silently forked into a provider dir.

//go:embed all:builtin
var builtinFS embed.FS

// builtinRoot is the embed path prefix. Kept as a constant so the walk and the
// trim agree.
const builtinRoot = "builtin"

// BuiltinLabel is the DirLabel of the built-in dir. Deliberately distinct from
// every provider label — including wick's own app name — so a shipped skill is
// never mistaken for one the user installed into a provider dir.
const BuiltinLabel = "built-in"

// builtinWarningMarker appears in every extracted SKILL.md. There is no UI for
// these skills, so this header is the only thing a person who opens the file
// sees telling them their edits will not survive a restart.
const builtinWarningMarker = "MANAGED BY WICK"

// builtinWarningHeader is prepended to each extracted SKILL.md. It sits ABOVE
// the frontmatter as an HTML comment: markdown renderers hide it, and the
// frontmatter parser is unaffected because it only reads the block after the
// first `---`.
const builtinWarningHeader = "<!-- " + builtinWarningMarker + " — DO NOT EDIT.\n" +
	"This file ships inside the wick binary and is rewritten from scratch every\n" +
	"time wick starts or skills are synced. Any change made here is lost.\n" +
	"To change it, edit internal/agents/skillsync/builtin/ in the wick repo. -->\n"

// builtinReadme documents the directory for anyone who finds it on disk.
const builtinReadme = `# Built-in wick skills — MANAGED BY WICK

Everything in this directory ships inside the wick binary. It is **deleted and
rewritten from scratch** every time wick starts and every time skills are
synced. Any file you add or edit here will be lost without warning.

These skills describe how to USE wick — connectors, plugins, workflows, agents.
They are read by wick's own agent; they are never copied into
` + "`~/.claude/skills`" + ` or any other provider's directory.

## Want to change one?

Edit the source in the wick repo at ` + "`internal/agents/skillsync/builtin/`" + `
and rebuild. There is no supported way to override a built-in skill on disk.

## Want your own skill?

Put it in your normal skills directory instead — that one is yours, and wick
never overwrites it.
`

// BuiltinDir is where the shipped skills are extracted: a sibling of the user's
// skills dir, under the same data root. Sibling rather than child so a stray
// recursive scan of the skills dir can never pick these up as user skills.
func BuiltinDir() string { return filepath.Join(appname.DataDir(), "builtin-skills") }

// ReadDirs returns every directory a skill may be READ from: the synced
// provider dirs plus wick's built-in dir.
//
// Callers that read skills (the agent's catalog, its filesystem read roots, the
// skills listing) must use this. Callers that SYNC must use KnownDirs, which
// omits the builtin dir — mixing the two would let a shipped skill be copied
// into a user's provider dir, where wick could no longer safely rewrite it.
//
// The dir is extracted on first use if it is missing, so a read path never
// depends on boot order having already run SyncBuiltin. Extraction is skipped
// once the dir exists; SyncBuiltin is what refreshes it.
func ReadDirs() []string {
	dirs := KnownDirs()
	bd := BuiltinDir()
	if bd == "" {
		return dirs
	}
	if fi, err := os.Stat(bd); err != nil || !fi.IsDir() {
		// Best-effort: a failure here just means no built-in skills this call.
		_, _ = SyncBuiltin()
	}
	if fi, err := os.Stat(bd); err == nil && fi.IsDir() {
		dirs = append(dirs, bd)
	}
	return dirs
}

// isBuiltinDir reports whether dir is wick's built-in skills dir.
func isBuiltinDir(dir string) bool { return dir == BuiltinDir() }

// SyncBuiltin wipes the built-in dir and rewrites it from the embedded copy.
//
// The wipe is what keeps the catalog honest: a plain overwrite only rewrites
// what the embed still contains, so a skill removed in a newer wick version
// would linger on disk forever and keep being offered to the agent. Deleting
// first is safe precisely because nothing but wick may write here.
//
// An empty embed is treated as "not built" rather than "no skills": the dir is
// left untouched and no error is returned, mirroring how a missing embedded
// gate binary degrades instead of destroying state.
func SyncBuiltin() (Result, error) {
	res := Result{}

	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil || len(entries) == 0 {
		// No embedded skills (e.g. a build that trimmed them). Leave whatever
		// is on disk alone — wiping here would delete the previous version's
		// skills and replace them with nothing.
		return res, nil
	}

	dir := BuiltinDir()
	res.Dirs = []string{dir}

	if err := os.RemoveAll(dir); err != nil {
		return res, fmt.Errorf("clear builtin skills dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("create builtin skills dir: %w", err)
	}

	skills := map[string]bool{}
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

	// Dot-prefixed so scan() skips it: this is a notice for humans who find the
	// directory, not a skill. A plain README.md would be listed as a top-level
	// entry and offered to the agent as something to invoke.
	if werr := os.WriteFile(filepath.Join(dir, ".README.md"), []byte(builtinReadme), 0o644); werr != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("write README: %v", werr))
	}
	res.SkillsCopied = len(skills)
	return res, nil
}
