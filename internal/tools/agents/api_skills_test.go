package agents

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/skillsync"
)

func TestBuildSkillListItems(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		input []skillsync.SkillFile
		want  []SkillListItem
	}{
		{
			name:  "empty input returns empty slice",
			input: []skillsync.SkillFile{},
			want:  []SkillListItem{},
		},
		{
			name: "nil Sources and Missing are coerced to empty slices",
			input: []skillsync.SkillFile{
				{Name: "foo.md", IsDir: false, Sources: nil, Missing: nil, Newest: now},
			},
			want: []SkillListItem{
				{Name: "foo.md", IsDir: false, InDirs: []string{}, MissingDirs: []string{}},
			},
		},
		{
			name: "directory entry with sources and missing",
			input: []skillsync.SkillFile{
				{
					Name:    "mypkg",
					IsDir:   true,
					Sources: []string{"/home/user/.claude/skills"},
					Missing: []string{"/home/user/.codex/skills"},
					Newest:  now,
				},
			},
			want: []SkillListItem{
				{
					Name:        "mypkg",
					IsDir:       true,
					InDirs:      []string{"/home/user/.claude/skills"},
					MissingDirs: []string{"/home/user/.codex/skills"},
				},
			},
		},
		{
			name: "multiple entries preserved in order",
			input: []skillsync.SkillFile{
				{Name: "a.md", IsDir: false, Sources: []string{"d1"}, Missing: []string{}, Newest: now},
				{Name: "b", IsDir: true, Sources: []string{"d1", "d2"}, Missing: []string{}, Newest: now},
			},
			want: []SkillListItem{
				{Name: "a.md", IsDir: false, InDirs: []string{"d1"}, MissingDirs: []string{}},
				{Name: "b", IsDir: true, InDirs: []string{"d1", "d2"}, MissingDirs: []string{}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSkillListItems(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i, g := range got {
				w := tc.want[i]
				if g.Name != w.Name {
					t.Errorf("[%d] Name = %q, want %q", i, g.Name, w.Name)
				}
				if g.IsDir != w.IsDir {
					t.Errorf("[%d] IsDir = %v, want %v", i, g.IsDir, w.IsDir)
				}
				if len(g.InDirs) != len(w.InDirs) {
					t.Errorf("[%d] InDirs len = %d, want %d", i, len(g.InDirs), len(w.InDirs))
				}
				if len(g.MissingDirs) != len(w.MissingDirs) {
					t.Errorf("[%d] MissingDirs len = %d, want %d", i, len(g.MissingDirs), len(w.MissingDirs))
				}
			}
		})
	}
}

func TestSkillEntriesToFiles(t *testing.T) {
	now := time.Now()
	entries := []skillsync.SkillEntry{
		{Name: "x", IsDir: false, Sources: []string{"d1"}, Missing: []string{"d2"}, Newest: now},
		{Name: "y", IsDir: true, Sources: []string{"d1", "d2"}, Missing: nil, Newest: now},
	}
	got := skillEntriesToFiles(entries)
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[0].Name != "x" || got[0].IsDir || got[0].Sources[0] != "d1" {
		t.Errorf("entry[0] mismatch: %+v", got[0])
	}
	if got[1].Name != "y" || !got[1].IsDir {
		t.Errorf("entry[1] mismatch: %+v", got[1])
	}
}
