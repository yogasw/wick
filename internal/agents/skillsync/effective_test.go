package skillsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEffectiveSkillsLocalWins(t *testing.T) {
	local := filepath.Join(t.TempDir(), ".claude", "skills")
	for _, n := range []string{"deploy", "wick-notes"} {
		if err := os.MkdirAll(filepath.Join(local, n), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(local, n, "SKILL.md"), []byte("---\nname: "+n+"\ndescription: mine\n---\n"), 0o644)
	}
	others := []SkillInfo{
		{Name: "wick-notes", Builtin: true, Meta: map[string]string{"description": "shipped"}},
		{Name: "wick-agent-cards", Builtin: true},
		{Name: "loki", Meta: map[string]string{"description": "logs"}},
	}
	got := EffectiveSkills(local, others)
	want := []EffectiveSkill{
		{Name: "deploy", Description: "mine", Source: SourceLocal},
		{Name: "loki", Description: "logs", Source: SourceGlobal},
		{Name: "wick-agent-cards", Source: SourceBuiltin, Required: true},
		{Name: "wick-notes", Description: "mine", Source: SourceLocal, Overrides: SourceBuiltin},
		{Name: "wick-notes", Description: "shipped", Source: SourceBuiltin, Shadowed: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := EffectiveSkills(filepath.Join(t.TempDir(), "none"), nil); len(got) != 0 {
		t.Fatalf("missing dir = %+v", got)
	}
}
