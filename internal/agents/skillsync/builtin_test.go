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
	if _, err := os.Stat(filepath.Join(dir, builtinStampName)); err != nil {
		t.Errorf("builtin stamp (the human-facing warning) missing: %v", err)
	}
	for _, s := range ListSkills() {
		if s.Name == builtinStampName || s.Name == "README.md" {
			t.Error("the builtin stamp file is being listed as a skill")
		}
	}
}

// TestSyncBuiltinRewritesOnlyChangedFiles: the shipped skills now share a
// directory with the user's own, so Sync resolves winners by mtime across it.
// Rewriting an unchanged file on every boot would keep bumping those mtimes and
// make every shipped skill perpetually "newest", so an unchanged file must be
// left completely alone.
func TestSyncBuiltinRewritesOnlyChangedFiles(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("first SyncBuiltin: %v", err)
	}
	md := filepath.Join(BuiltinDir(), "wick-connectors", "SKILL.md")
	before, err := os.Stat(md)
	if err != nil {
		t.Fatalf("stat %s: %v", md, err)
	}

	res, err := SyncBuiltin()
	if err != nil {
		t.Fatalf("second SyncBuiltin: %v", err)
	}
	if res.Copied != 0 {
		t.Errorf("second sync rewrote %d unchanged files; want 0", res.Copied)
	}
	after, err := os.Stat(md)
	if err != nil {
		t.Fatalf("stat %s: %v", md, err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("unchanged file mtime moved: %v → %v", before.ModTime(), after.ModTime())
	}
}

// TestSyncBuiltinRepairsEditedFile: the flip side of the test above. An edited
// shipped file differs by content hash and must be restored, or the "rewritten
// on every start" promise in its own header would be false.
func TestSyncBuiltinRepairsEditedFile(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("first SyncBuiltin: %v", err)
	}
	md := filepath.Join(BuiltinDir(), "wick-connectors", "SKILL.md")
	if err := os.WriteFile(md, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("second SyncBuiltin: %v", err)
	}
	data, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tampered") {
		t.Error("edited shipped file was not restored")
	}
	if !strings.Contains(string(data), builtinWarningMarker) {
		t.Error("restored file lost its warning header")
	}
}

// TestSyncBuiltinKeepsUserSkillsInSameDir: the shipped skills share wick's own
// skills dir with the user's. A wipe-and-rewrite would be the simplest way to
// drop stale shipped skills and would destroy the user's work, so pruning must
// touch only what wick itself ships.
func TestSyncBuiltinKeepsUserSkillsInSameDir(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("first SyncBuiltin: %v", err)
	}
	wickDir := BuiltinDir()
	writeSkill(t, wickDir, "my-own-skill", "---\nname: my-own-skill\n---\nbody\n", time.Now(), nil)

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("second SyncBuiltin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wickDir, "my-own-skill", "SKILL.md")); err != nil {
		t.Errorf("a user skill in wick's own dir was destroyed by SyncBuiltin: %v", err)
	}
}

// TestSyncBuiltinRemovesStaleSkills: a skill dropped from a newer wick version
// must disappear. Only rewriting what the embed still has would leave an
// obsolete skill on disk forever, still showing up in the agent's catalog.
//
// The marker is what makes this safe to do in a shared directory: it identifies
// a folder wick wrote, so pruning can never reach a user's own skill.
func TestSyncBuiltinRemovesStaleSkills(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("first SyncBuiltin: %v", err)
	}
	stale := filepath.Join(BuiltinDir(), "removed-in-newer-version")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	body := builtinWarningHeader + "---\nname: removed-in-newer-version\n---\nold\n"
	if err := os.WriteFile(filepath.Join(stale, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("second SyncBuiltin: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale builtin skill survived a resync")
	}
}

// TestSyncBuiltinKeepsAdoptedSkill: a user who takes over a shipped skill by
// rewriting its managed header owns that copy. Pruning must leave it alone even
// once wick stops shipping a skill by that name — deleting it would destroy
// work wick has no claim on.
func TestSyncBuiltinKeepsAdoptedSkill(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("first SyncBuiltin: %v", err)
	}
	adopted := filepath.Join(BuiltinDir(), "adopted-from-an-old-version")
	if err := os.MkdirAll(adopted, 0o755); err != nil {
		t.Fatal(err)
	}
	// No managed marker: the user rewrote the file as their own.
	md := filepath.Join(adopted, "SKILL.md")
	if err := os.WriteFile(md, []byte("---\nname: adopted\n---\nmine now\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("second SyncBuiltin: %v", err)
	}
	data, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("adopted skill was deleted: %v", err)
	}
	if !strings.Contains(string(data), "mine now") {
		t.Error("adopted skill was overwritten")
	}
}

// TestSyncBuiltinMirroredToProviders: shipped skills are mirrored INTO each
// provider's skills dir, because a CLI provider resolves a slash command from
// its own dir only — without the copy, `/wick-notes` is "Unknown command".
//
// The copy is wick's to own wherever it lands: it carries the managed marker so
// the next sync can rewrite it and a later prune can remove it.
func TestSyncBuiltinMirroredToProviders(t *testing.T) {
	home, claudeDir, wickDir := stageHome(t)
	codexDir := filepath.Join(home, ".codex", "skills")

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}

	names := BuiltinNames()
	if len(names) == 0 {
		t.Skip("no shipped skills in this build")
	}
	for name := range names {
		for _, dir := range []string{claudeDir, codexDir} {
			md := filepath.Join(dir, name, "SKILL.md")
			data, err := os.ReadFile(md)
			if err != nil {
				t.Errorf("builtin %q not mirrored into %s: %v", name, dir, err)
				continue
			}
			if !strings.Contains(string(data), builtinWarningMarker) {
				t.Errorf("mirrored %q in %s lacks the managed marker, so it could never be rewritten or pruned", name, dir)
			}
		}
	}

	// Only wick's own dir is the canonical home: it carries the stamp/readme,
	// and a provider mirror must not claim to be one.
	if _, err := os.Stat(filepath.Join(wickDir, builtinStampName)); err != nil {
		t.Errorf("wick's own dir lost the builtin stamp: %v", err)
	}
	for _, dir := range []string{claudeDir, codexDir} {
		if _, err := os.Stat(filepath.Join(dir, builtinStampName)); err == nil {
			t.Errorf("mirror dir %s should not carry the builtin stamp", dir)
		}
	}
}

// TestSyncBuiltinMirrorPrunesDroppedSkill: the objection the mirror had to
// answer. A skill wick no longer ships must be deleted from the provider dirs
// too, or a stale copy would keep answering a slash command forever.
func TestSyncBuiltinMirrorPrunesDroppedSkill(t *testing.T) {
	_, claudeDir, _ := stageHome(t)

	// A folder that looks like a skill wick shipped in an older version:
	// managed marker present, but absent from the current embed.
	dropped := filepath.Join(claudeDir, "wick-dropped-in-an-old-version")
	if err := os.MkdirAll(dropped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropped, "SKILL.md"),
		[]byte(builtinWarningHeader+"---\nname: dropped\n---\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A user's own skill in the same dir, with no marker.
	mine := filepath.Join(claudeDir, "my-own-skill")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine, "SKILL.md"),
		[]byte("---\nname: mine\n---\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}

	if _, err := os.Stat(dropped); !os.IsNotExist(err) {
		t.Error("a dropped builtin was left behind in the mirror dir")
	}
	if _, err := os.Stat(filepath.Join(mine, "SKILL.md")); err != nil {
		t.Errorf("a user's own skill was pruned from the mirror dir: %v", err)
	}
}

// TestSyncKeepsBuiltinsOutOfTheRotation: mirroring is one-directional. Sync()
// must still refuse to treat a shipped skill as a rotation member, so a
// provider copy never wins the mtime race against the binary (which would
// silently revert on the next boot) and never appears twice in the skills UI.
func TestSyncKeepsBuiltinsOutOfTheRotation(t *testing.T) {
	_, claudeDir, _ := stageHome(t)

	writeSkill(t, claudeDir, "user-skill", "body\n", time.Now().Add(-time.Hour), nil)
	if _, err := Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// A user skill still syncs normally — the exclusion is scoped to shipped
	// names, not to the dir they happen to share.
	if _, err := os.Stat(filepath.Join(BuiltinDir(), "user-skill")); err != nil {
		t.Errorf("a normal user skill stopped syncing into wick's dir: %v", err)
	}
	// And a shipped name is still rejected by the single-entry path.
	for name := range BuiltinNames() {
		if _, err := SyncEntry(name); err == nil {
			t.Errorf("SyncEntry(%q) should refuse a shipped skill", name)
		}
		break
	}
}

// TestSyncEntryRefusesBuiltin: syncing ONE entry is a separate code path from
// Sync, and an explicit push is a third. Both must refuse a shipped skill for
// the same reason Sync skips it — a copy outside wick's dir is unmanageable.
func TestSyncEntryRefusesBuiltin(t *testing.T) {
	stageHome(t)
	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	if _, err := SyncEntry("wick-connectors"); err == nil {
		t.Error("SyncEntry mirrored a shipped skill into the provider dirs")
	}
	if _, err := PushFrom(BuiltinDir(), "wick-connectors"); err == nil {
		t.Error("PushFrom pushed a shipped skill into the provider dirs")
	}
}

// TestReadDirsIncludesBuiltin: read paths must reach the shipped skills so the
// agent can open a SKILL.md the catalog points at.
func TestReadDirsIncludesBuiltin(t *testing.T) {
	setTestHome(t, t.TempDir())

	read := ReadDirs()
	if !containsDir(read, BuiltinDir()) {
		t.Errorf("ReadDirs missing builtin dir %q; got %v", BuiltinDir(), read)
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

// TestBuiltinCatalogNamesEverySkillWithPath: the CLI providers never see the
// shipped skills through their own loader — the skills are not copied into
// ~/.claude/skills or ~/.codex/skills — so this block is the only thing that
// tells the agent they exist. Every skill needs a name AND an absolute path:
// without the path the agent has nothing to open, and the --add-dir that makes
// the path readable would be pointing at nothing anyone was told to read.
func TestBuiltinCatalogNamesEverySkillWithPath(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	cat := BuiltinCatalog()
	if cat == "" {
		t.Fatal("catalog is empty — the agent would never learn the shipped skills exist")
	}
	for name := range BuiltinNames() {
		want := filepath.Join(BuiltinDir(), name, "SKILL.md")
		if !strings.Contains(cat, filepath.ToSlash(want)) && !strings.Contains(cat, want) {
			t.Errorf("catalog omits a readable path for %q", name)
		}
	}
}

// TestAppendBuiltinCatalogPreservesPreset: the operator's preset opens with the
// session identity block, which the CLIs and the operator both expect at the
// top, so the catalog must be appended rather than prepended.
func TestAppendBuiltinCatalogPreservesPreset(t *testing.T) {
	setTestHome(t, t.TempDir())

	if _, err := SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	const preset = "session_id: abc\n\nYou are a helpful agent.\n"
	got := AppendBuiltinCatalog(preset)
	if !strings.HasPrefix(got, "session_id: abc") {
		t.Errorf("preset no longer leads the prompt: %.40q", got)
	}
	if !strings.Contains(got, "Built-in wick skills") {
		t.Error("catalog missing from the combined prompt")
	}
	// A bare agent has no preset and is exactly the one that most needs to be
	// told the shipped skills exist.
	if bare := AppendBuiltinCatalog(""); !strings.Contains(bare, "Built-in wick skills") {
		t.Error("empty preset dropped the catalog")
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
