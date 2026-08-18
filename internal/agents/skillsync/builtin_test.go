package skillsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSyncBuiltinWritesEverySkill: the embedded skills must land on disk under
// wick's own builtin dir, ready for the agent to read.
func TestSyncBuiltinWritesEverySkill(t *testing.T) {
	setTestHome(t, t.TempDir())

	res, err := SyncBuiltin()
	if err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("SyncBuiltin errors: %v", res.Errors)
	}
	if res.SkillsCopied == 0 {
		t.Fatal("no builtin skills written — the embed is empty or unreadable")
	}

	dir := BuiltinDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read builtin dir: %v", err)
	}
	skills := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skills++
		md := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(md)
		if err != nil {
			t.Errorf("%s: %v", md, err)
			continue
		}
		body := string(data)
		// Every extracted SKILL.md carries the overwrite warning, because
		// without a UI this header is the only place a reader who opens the
		// file learns their edits will not survive a restart.
		if !strings.Contains(body, builtinWarningMarker) {
			t.Errorf("%s missing overwrite warning header", md)
		}
		// The warning must sit ABOVE the frontmatter, or it would be parsed
		// as part of it and break name/description resolution.
		if idx := strings.Index(body, "---"); idx >= 0 {
			if strings.Index(body, builtinWarningMarker) > idx {
				t.Errorf("%s: warning must precede frontmatter", md)
			}
		}
	}
	if skills != res.SkillsCopied {
		t.Errorf("wrote %d skill dirs, reported %d", skills, res.SkillsCopied)
	}
	// Dot-prefixed: present for a human who opens the directory, but skipped by
	// scan() so it is never offered to the agent as a skill.
	if _, err := os.Stat(filepath.Join(dir, ".README.md")); err != nil {
		t.Errorf("builtin dir README (the human-facing warning) missing: %v", err)
	}
	for _, s := range ListSkills() {
		if s.Name == "README.md" || s.Name == ".README.md" {
			t.Error("the builtin dir README is being listed as a skill")
		}
	}
}

// TestSyncBuiltinRemovesStaleSkills: a skill dropped from a newer wick version
// must disappear. "Overwrite everything" only rewrites what the embed still
// has, so without an explicit wipe an obsolete skill would linger forever and
// keep showing up in the agent's catalog.
func TestSyncBuiltinRemovesStaleSkills(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("first SyncBuiltin: %v", err)
	}
	stale := filepath.Join(BuiltinDir(), "removed-in-newer-version")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "SKILL.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("second SyncBuiltin: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale builtin skill survived a resync")
	}
}

// TestSyncBuiltinIsolatedFromUserSkills is the boundary that makes read-only
// enforceable: Sync() must never copy a builtin skill into the user's skill
// dirs, and must never copy a user skill into the builtin dir.
func TestSyncBuiltinIsolatedFromUserSkills(t *testing.T) {
	_, claudeDir, wickDir := stageHome(t)

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	builtinNames, err := os.ReadDir(BuiltinDir())
	if err != nil {
		t.Fatalf("read builtin dir: %v", err)
	}

	writeSkill(t, claudeDir, "user-skill", "body\n", time.Now().Add(-time.Hour), nil)
	if _, err := Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// A user skill must not leak into the builtin dir...
	if _, err := os.Stat(filepath.Join(BuiltinDir(), "user-skill")); !os.IsNotExist(err) {
		t.Error("Sync copied a user skill into the builtin dir")
	}
	// ...and no builtin skill may leak into a provider dir.
	for _, e := range builtinNames {
		if !e.IsDir() {
			continue
		}
		for _, d := range []string{claudeDir, wickDir} {
			if _, err := os.Stat(filepath.Join(d, e.Name())); !os.IsNotExist(err) {
				t.Errorf("builtin skill %q leaked into %s", e.Name(), d)
			}
		}
	}
}

// TestReadDirsIncludesBuiltin: read paths see the builtin dir (so the agent can
// open a SKILL.md), while the sync rotation does not.
func TestReadDirsIncludesBuiltin(t *testing.T) {
	setTestHome(t, t.TempDir())

	read := ReadDirs()
	if !containsDir(read, BuiltinDir()) {
		t.Errorf("ReadDirs missing builtin dir %q; got %v", BuiltinDir(), read)
	}
	if containsDir(KnownDirs(), BuiltinDir()) {
		t.Error("KnownDirs must NOT include the builtin dir — it would join the sync rotation")
	}
}

// TestListSkillsFlagsBuiltin: the flag is what lets callers tell a shipped
// skill from a user's own without consulting a manifest.
func TestListSkillsFlagsBuiltin(t *testing.T) {
	_, claudeDir, _ := stageHome(t)

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	writeSkill(t, claudeDir, "mine", "---\nname: mine\n---\n", time.Now(), nil)

	var sawBuiltin, sawUser bool
	for _, s := range ListSkills() {
		if s.Name == "mine" {
			sawUser = true
			if s.Builtin {
				t.Error("user skill wrongly flagged builtin")
			}
			continue
		}
		if s.IsDir && s.Builtin {
			sawBuiltin = true
		}
	}
	if !sawBuiltin {
		t.Error("no skill flagged Builtin")
	}
	if !sawUser {
		t.Error("user skill missing from ListSkills")
	}
}

func containsDir(dirs []string, want string) bool {
	for _, d := range dirs {
		if d == want {
			return true
		}
	}
	return false
}

// TestBuiltinFrontmatterParses: the overwrite-warning header must not hide the
// frontmatter. It sits above the `---` block as an HTML comment, so a parser
// that requires `---` at byte zero silently returns no metadata — leaving every
// shipped skill with an empty name and description in the agent's catalog.
func TestBuiltinFrontmatterParses(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	n := 0
	for _, s := range ListSkills() {
		if !s.Builtin || !s.IsDir {
			continue
		}
		n++
		if strings.TrimSpace(s.Meta["name"]) == "" {
			t.Errorf("builtin skill %q has no frontmatter name", s.Name)
		}
		if strings.TrimSpace(s.Meta["description"]) == "" {
			t.Errorf("builtin skill %q has no frontmatter description", s.Name)
		}
	}
	if n == 0 {
		t.Fatal("no builtin skills found")
	}
}

// TestParseFrontmatterSkipsLeadingComment pins the rule directly.
func TestParseFrontmatterSkipsLeadingComment(t *testing.T) {
	doc := "<!-- a warning\nspanning lines -->\n---\nname: x\ndescription: d\n---\n\nbody\n"
	meta := parseFrontmatter([]byte(doc))
	if meta["name"] != "x" {
		t.Errorf("name = %q, want \"x\"", meta["name"])
	}
	if meta["description"] != "d" {
		t.Errorf("description = %q, want \"d\"", meta["description"])
	}
}

// TestDirLabelForBuiltin: the builtin dir must get a real label. It is a
// sibling of the skills dir (~/.<app>/builtin-skills), so the generic
// "strip the leading dot, take the first path segment" rule yields "" and the
// fallback yields "." — neither identifies anything.
func TestDirLabelForBuiltin(t *testing.T) {
	setTestHome(t, t.TempDir())

	got := DirLabel(BuiltinDir())
	if got == "" || got == "." {
		t.Fatalf("DirLabel(builtin) = %q — not a usable label", got)
	}
	if got == OwnLabel() {
		t.Errorf("builtin label %q collides with the user skills dir label", got)
	}
}

// TestParseFrontmatterFoldedScalar: YAML folded/literal block scalars are
// common in hand-written skills (a long description wrapped over several
// indented lines). Reading only the text after the colon yields ">-", which
// then shows up verbatim as the skill's description everywhere it is listed.
func TestParseFrontmatterFoldedScalar(t *testing.T) {
	doc := "---\nname: x\ndescription: >-\n  first line of the summary\n  continued on the next\nversion: 1\n---\n\nbody\n"
	meta := parseFrontmatter([]byte(doc))
	want := "first line of the summary continued on the next"
	if meta["description"] != want {
		t.Errorf("description = %q, want %q", meta["description"], want)
	}
	if meta["name"] != "x" {
		t.Errorf("name = %q, want \"x\"", meta["name"])
	}
	if meta["version"] != "1" {
		t.Errorf("version = %q, want \"1\"", meta["version"])
	}
}

// TestBuiltinMetaWinsOverSameNamedUserSkill: a user skill may share a name with
// a shipped one. Metadata must then come from the BUILT-IN copy — it is the one
// wick controls and the one whose description describes the shipped behaviour.
func TestBuiltinMetaWinsOverSameNamedUserSkill(t *testing.T) {
	_, claudeDir, _ := stageHome(t)
	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}

	// Shadow a shipped skill with a same-named user one.
	writeSkill(t, claudeDir, "wick-connectors",
		"---\nname: wick-connectors\ndescription: a totally different thing\n---\n",
		time.Now(), nil)

	for _, s := range ListSkills() {
		if s.Name != "wick-connectors" {
			continue
		}
		if !s.Builtin {
			t.Fatal("shadowed skill lost its Builtin flag")
		}
		if strings.Contains(s.Meta["description"], "a totally different thing") {
			t.Errorf("metadata came from the user copy: %q", s.Meta["description"])
		}
		return
	}
	t.Fatal("wick-connectors not listed")
}
