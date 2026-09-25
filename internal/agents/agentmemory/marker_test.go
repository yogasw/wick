package agentmemory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readMarker(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	return string(b)
}

// TestEnsureMarkerCreates is the core fix from PLAN §14.4: a wick project cwd
// is named "files", so without this file every project on the host resolves to
// the same ai-memory project.
func TestEnsureMarkerCreates(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureMarker(dir, Scope{Workspace: "wick", Project: "kasir-8c28230d"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := readMarker(t, dir)
	for _, want := range []string{`workspace = "wick"`, `project = "kasir-8c28230d"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("marker missing %q:\n%s", want, got)
		}
	}
}

// TestEnsureMarkerIdempotent: a second call must not rewrite, duplicate a key,
// or grow the file. The hook runs on every project create AND every session
// move, so "called again" is the normal case, not an edge one.
func TestEnsureMarkerIdempotent(t *testing.T) {
	dir := t.TempDir()
	sc := Scope{Workspace: "wick", Project: "kasir-8c28230d"}
	if err := EnsureMarker(dir, sc); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	first := readMarker(t, dir)
	if err := EnsureMarker(dir, sc); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if second := readMarker(t, dir); second != first {
		t.Fatalf("second ensure rewrote the file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if n := strings.Count(first, "project ="); n != 1 {
		t.Fatalf("project key written %d times:\n%s", n, first)
	}
}

// TestEnsureMarkerKeepsExistingScope: a name already in the file wins. That is
// what lets a human pin their own workspace, and what stops a project rename
// from silently moving its memory to a new bucket.
func TestEnsureMarkerKeepsExistingScope(t *testing.T) {
	dir := t.TempDir()
	orig := "workspace = \"qiscus\"\nproject = \"pinned-by-hand\"\n"
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureMarker(dir, Scope{Workspace: "wick", Project: "auto-123abc45"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := readMarker(t, dir)
	if got != orig {
		t.Fatalf("existing scope was overwritten:\n%s", got)
	}
}

// TestEnsureMarkerAdditive: everything a human put in the file survives, and a
// missing root key is inserted ABOVE the first [section] — a key written after
// a header would belong to that table, not to the document root.
func TestEnsureMarkerAdditive(t *testing.T) {
	dir := t.TempDir()
	orig := "# pinned by ops\n" +
		"drop_subagent_captures = \"true\"\n" +
		"\n" +
		"[capture]\n" +
		"ignore_paths = [\"secrets/\"]\n"
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureMarker(dir, Scope{Workspace: "wick", Project: "kasir-8c28230d"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := readMarker(t, dir)
	for _, want := range []string{
		"# pinned by ops",
		`drop_subagent_captures = "true"`,
		"[capture]",
		`ignore_paths = ["secrets/"]`,
		`workspace = "wick"`,
		`project = "kasir-8c28230d"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("additive write lost %q:\n%s", want, got)
		}
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	var scopeAt, sectionAt = -1, -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "project =") {
			scopeAt = i
		}
		if strings.TrimSpace(l) == "[capture]" && sectionAt < 0 {
			sectionAt = i
		}
	}
	if scopeAt < 0 || sectionAt < 0 || scopeAt > sectionAt {
		t.Fatalf("scope key must sit above [capture] (project=%d, section=%d):\n%s", scopeAt, sectionAt, got)
	}
}

// TestEnsureMarkerIgnoresSectionKey: a `project` nested in [capture] is a
// different key and must not be mistaken for the document-root scope.
func TestEnsureMarkerIgnoresSectionKey(t *testing.T) {
	dir := t.TempDir()
	orig := "[capture]\nproject = \"not-the-scope\"\n"
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureMarker(dir, Scope{Workspace: "wick", Project: "kasir-8c28230d"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := readMarker(t, dir)
	if !strings.HasPrefix(got, "workspace = \"wick\"\nproject = \"kasir-8c28230d\"\n") {
		t.Fatalf("root scope not inserted above the section:\n%s", got)
	}
	if !strings.Contains(got, `project = "not-the-scope"`) {
		t.Fatalf("section key was clobbered:\n%s", got)
	}
}

func TestEnsureMarkerRejectsIncompleteScope(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureMarker(dir, Scope{Workspace: "wick"}); err == nil {
		t.Fatal("empty project name must be refused, not written")
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerName)); !os.IsNotExist(err) {
		t.Fatalf("a marker was written for an incomplete scope: %v", err)
	}
}

// TestSanitizeName pins the alphabet the ai-memory server validates against:
// lowercase letters, digits and dashes only.
func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Kasir Nusantara", "kasir-nusantara"},
		{"  spaced  out  ", "spaced-out"},
		{"BTPN/Jenius (prod)", "btpn-jenius-prod"},
		{"emoji 🚀 name", "emoji-name"},
		{"--already--dashed--", "already-dashed"},
		{"under_score.dot", "under-score-dot"},
		{"8c28230d-3c7e-42e3", "8c28230d-3c7e-42e3"},
		{"   ", ""},
		{"🚀🚀", ""},
	}
	for _, tc := range cases {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := sanitizeName(strings.Repeat("a", maxNameLen+40))
	if len(long) != maxNameLen {
		t.Errorf("long name not capped: len=%d", len(long))
	}
}

// TestProjectScope pins the wick-project → ai-memory-scope mapping: a readable
// slug for the human, the uuid prefix for the collision safety §14.3 is about.
func TestProjectScope(t *testing.T) {
	sc, err := ProjectScope("wick", "Kasir Nusantara", "8c28230d-3c7e-42e3-ba46-2dd7e7c5ff0e")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	if sc.Workspace != "wick" || sc.Project != "kasir-nusantara-8c28230d" {
		t.Fatalf("unexpected scope %+v", sc)
	}

	// Two unrelated projects that share a display name must NOT share a bucket
	// — that silent merge is the whole reason the marker exists.
	a, _ := ProjectScope("wick", "backend", "11111111-aaaa-bbbb-cccc-dddddddddddd")
	b, _ := ProjectScope("wick", "backend", "22222222-aaaa-bbbb-cccc-dddddddddddd")
	if a.Project == b.Project {
		t.Fatalf("name collision not separated: both resolve to %q", a.Project)
	}

	// A name that sanitises to nothing still yields a usable, unique project.
	only, err := ProjectScope("wick", "🚀", "8c28230d-3c7e-42e3-ba46-2dd7e7c5ff0e")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	if only.Project != "8c28230d" {
		t.Fatalf("unnamed project should fall back to the id prefix, got %q", only.Project)
	}

	// No usable id = an error, never a shared fallback name.
	if _, err := ProjectScope("wick", "backend", "🚀"); err == nil {
		t.Fatal("unusable project id must be refused")
	}
	if _, err := ProjectScope("  ", "backend", "8c28230d"); err == nil {
		t.Fatal("unusable workspace must be refused")
	}
}

// TestMarkerWriterGated: nothing is written while the feature is off — a host
// that doesn't use Agent Memory gets no wick dotfiles in its project folders.
func TestMarkerWriterGated(t *testing.T) {
	dir := t.TempDir()
	off := MarkerWriter{Workspace: "wick", Enabled: func() bool { return false }}
	if err := off.Ensure(dir, "Kasir", "8c28230d-3c7e"); err != nil {
		t.Fatalf("gated ensure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerName)); !os.IsNotExist(err) {
		t.Fatalf("marker written while the feature is off: %v", err)
	}

	// Unwired (nil Enabled) is the same as off.
	if err := (MarkerWriter{Workspace: "wick"}).Ensure(dir, "Kasir", "8c28230d-3c7e"); err != nil {
		t.Fatalf("unwired ensure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerName)); !os.IsNotExist(err) {
		t.Fatalf("marker written by an unwired writer: %v", err)
	}

	on := MarkerWriter{Workspace: "wick", Enabled: func() bool { return true }}
	if err := on.Ensure(dir, "Kasir", "8c28230d-3c7e"); err != nil {
		t.Fatalf("enabled ensure: %v", err)
	}
	if !strings.Contains(readMarker(t, dir), `project = "kasir-8c28230d"`) {
		t.Fatalf("enabled writer did not pin the scope:\n%s", readMarker(t, dir))
	}
}
