package skillsync

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type zipEntry struct {
	name string
	body string
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", e.name, err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("write zip entry %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func planFromEntries(t *testing.T, stem string, entries []zipEntry) (string, []zipEntryPlan, error) {
	t.Helper()
	data := buildZip(t, entries)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	return planZipExtraction(stem, zr.File)
}

func destSet(plan []zipEntryPlan) map[string]bool {
	out := map[string]bool{}
	for _, p := range plan {
		out[p.dest] = true
	}
	return out
}

func TestPlanZipExtraction_SingleRootFolder(t *testing.T) {
	folder, plan, err := planFromEntries(t, "archive", []zipEntry{
		{"bitbucket-pr-review/SKILL.md", "---\nname: x\n---\n"},
		{"bitbucket-pr-review/rules/go-rules.md", "go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "bitbucket-pr-review" {
		t.Fatalf("folder = %q, want bitbucket-pr-review", folder)
	}
	got := destSet(plan)
	for _, want := range []string{"bitbucket-pr-review/SKILL.md", "bitbucket-pr-review/rules/go-rules.md"} {
		if !got[want] {
			t.Fatalf("missing dest %q in %v", want, got)
		}
	}
	if len(plan) != 2 {
		t.Fatalf("plan len = %d, want 2", len(plan))
	}
}

func TestPlanZipExtraction_TopLevelFiles(t *testing.T) {
	folder, plan, err := planFromEntries(t, "my-skill", []zipEntry{
		{"SKILL.md", "---\nname: x\n---\n"},
		{"rules/go-rules.md", "go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "my-skill" {
		t.Fatalf("folder = %q, want my-skill", folder)
	}
	got := destSet(plan)
	for _, want := range []string{"my-skill/SKILL.md", "my-skill/rules/go-rules.md"} {
		if !got[want] {
			t.Fatalf("missing dest %q in %v", want, got)
		}
	}
}

func TestPlanZipExtraction_FiltersMacOSJunk(t *testing.T) {
	folder, plan, err := planFromEntries(t, "archive", []zipEntry{
		{"__MACOSX/", ""},
		{"__MACOSX/._SKILL.md", "junk"},
		{"bitbucket-pr-review/.DS_Store", "junk"},
		{"bitbucket-pr-review/SKILL.md", "---\nname: x\n---\n"},
		{"bitbucket-pr-review/rules/go-rules.md", "go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "bitbucket-pr-review" {
		t.Fatalf("folder = %q, want bitbucket-pr-review", folder)
	}
	got := destSet(plan)
	if len(plan) != 2 {
		t.Fatalf("plan len = %d, want 2 (junk should be dropped): %v", len(plan), got)
	}
	for _, want := range []string{"bitbucket-pr-review/SKILL.md", "bitbucket-pr-review/rules/go-rules.md"} {
		if !got[want] {
			t.Fatalf("missing dest %q in %v", want, got)
		}
	}
}

func TestPlanZipExtraction_DoubleNested(t *testing.T) {
	folder, plan, err := planFromEntries(t, "archive", []zipEntry{
		{"outer/bitbucket-pr-review/SKILL.md", "---\nname: x\n---\n"},
		{"outer/bitbucket-pr-review/rules/x.md", "x"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "bitbucket-pr-review" {
		t.Fatalf("folder = %q, want bitbucket-pr-review", folder)
	}
	got := destSet(plan)
	for _, want := range []string{"bitbucket-pr-review/SKILL.md", "bitbucket-pr-review/rules/x.md"} {
		if !got[want] {
			t.Fatalf("missing dest %q in %v", want, got)
		}
	}
}

func TestPlanZipExtraction_FiltersWindowsJunk(t *testing.T) {
	folder, plan, err := planFromEntries(t, "archive", []zipEntry{
		{"bitbucket-pr-review/Thumbs.db", "junk"},
		{"bitbucket-pr-review/desktop.ini", "junk"},
		{"bitbucket-pr-review/SKILL.md", "---\nname: x\n---\n"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "bitbucket-pr-review" {
		t.Fatalf("folder = %q, want bitbucket-pr-review", folder)
	}
	if len(plan) != 1 || !destSet(plan)["bitbucket-pr-review/SKILL.md"] {
		t.Fatalf("plan = %v, want only bitbucket-pr-review/SKILL.md", destSet(plan))
	}
}

func TestPlanZipExtraction_NoUsableFiles(t *testing.T) {
	_, _, err := planFromEntries(t, "archive", []zipEntry{
		{"__MACOSX/", ""},
		{"__MACOSX/._x", "junk"},
	})
	if err == nil {
		t.Fatalf("expected error for zip with no usable files")
	}
}

func TestPlanZipExtraction_RejectsTraversal(t *testing.T) {
	_, _, err := planFromEntries(t, "archive", []zipEntry{
		{"../evil.md", "x"},
	})
	if err == nil {
		t.Fatalf("expected error for path traversal entry")
	}
}

func TestPlanZipExtraction_NoMetadataFallbackSingleRoot(t *testing.T) {
	folder, plan, err := planFromEntries(t, "archive", []zipEntry{
		{"myskill/notes.md", "n"},
		{"myskill/data/x.txt", "x"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "myskill" {
		t.Fatalf("folder = %q, want myskill", folder)
	}
	got := destSet(plan)
	for _, want := range []string{"myskill/notes.md", "myskill/data/x.txt"} {
		if !got[want] {
			t.Fatalf("missing dest %q in %v", want, got)
		}
	}
}

func TestPlanZipExtraction_NoMetadataFallbackMultiRoot(t *testing.T) {
	folder, plan, err := planFromEntries(t, "bundle", []zipEntry{
		{"a.md", "a"},
		{"b/c.md", "c"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "bundle" {
		t.Fatalf("folder = %q, want bundle", folder)
	}
	got := destSet(plan)
	for _, want := range []string{"bundle/a.md", "bundle/b/c.md"} {
		if !got[want] {
			t.Fatalf("missing dest %q in %v", want, got)
		}
	}
}

func TestUploadProcessed_ZipImportsToClaudeSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := buildZip(t, []zipEntry{
		{"bitbucket-pr-review/SKILL.md", "---\nname: bitbucket-pr-review\n---\n"},
		{"bitbucket-pr-review/rules/go-rules.md", "go"},
	})
	folder, res, err := UploadProcessed("bitbucket-pr-review.zip", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "bitbucket-pr-review" {
		t.Fatalf("folder = %q, want bitbucket-pr-review", folder)
	}
	if res.Copied == 0 {
		t.Fatalf("Copied = 0, want > 0")
	}
	skillMd := filepath.Join(home, ".claude", "skills", "bitbucket-pr-review", "SKILL.md")
	if _, err := os.Stat(skillMd); err != nil {
		t.Fatalf("expected SKILL.md at %s: %v", skillMd, err)
	}
}

func TestUploadProcessed_CreatesDefaultDirWhenNoneExist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	folder, res, err := UploadProcessed("notes.md", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder != "notes" {
		t.Fatalf("folder = %q, want notes", folder)
	}
	if res.Copied == 0 {
		t.Fatalf("Copied = 0, want > 0 (default dir should be created)")
	}
	dst := filepath.Join(home, ".claude", "skills", "notes", "SKILL.md")
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected default-dir write at %s: %v", dst, err)
	}
}
