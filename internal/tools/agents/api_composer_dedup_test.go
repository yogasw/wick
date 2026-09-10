package agents

import "testing"

// The `/` menu lists a skill by its invokable name, and that name is not
// unique: the same skill is mirrored into every provider's skills dir, and a
// folder copied from another one keeps the original's frontmatter name. The
// composer keys its menu rows on that name, and a duplicate key aborts the
// whole keyed each block — so a single clash rendered the menu EMPTY rather
// than merely repeating a row. De-duping by name is what keeps the keys unique.
func TestComposerSkillDedupeByName(t *testing.T) {
	// folder = directory on disk, name = frontmatter `name:` ("" = absent, in
	// which case the handler falls back to the folder name).
	rows := []struct{ folder, name string }{
		{"alpha-tool", "alpha-tool"},
		{"alpha-copy", "alpha-tool"}, // copied folder that kept the original name
		{"beta-tool", "beta-tool"},
		{"beta-tool", "beta-tool"}, // same skill mirrored into another provider dir
		{"gamma-tool", ""},         // no frontmatter name -> folder name
		{"gamma-tool", ""},         // ...and its mirror
	}

	seen := map[string]bool{}
	var kept []string
	for _, r := range rows {
		name := r.name
		if name == "" {
			name = r.folder
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		kept = append(kept, name)
	}

	want := []string{"alpha-tool", "beta-tool", "gamma-tool"}
	if len(kept) != len(want) {
		t.Fatalf("kept %v, want %v", kept, want)
	}
	for i := range want {
		if kept[i] != want[i] {
			t.Errorf("kept[%d] = %q, want %q", i, kept[i], want[i])
		}
	}

	// The property that actually matters: no name repeats, so no menu key
	// collides and the each block cannot abort.
	counts := map[string]int{}
	for _, n := range kept {
		counts[n]++
	}
	for n, c := range counts {
		if c > 1 {
			t.Errorf("name %q still appears %d times — the menu would abort again", n, c)
		}
	}
}
