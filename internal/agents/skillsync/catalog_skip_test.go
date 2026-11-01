package skillsync

import (
	"slices"
	"strings"
	"testing"
)

func TestIsRequiredSkill(t *testing.T) {
	if !IsRequiredSkill("wick-agent-cards") || IsRequiredSkill("wick-notes") {
		t.Fatal("required skill set")
	}
}

func TestWithAdminSkillsOff(t *testing.T) {
	in := []string{"wick-notes"}
	if got := WithAdminSkillsOff(in, true); !slices.Equal(got, in) {
		t.Errorf("with Bash: got %v, want %v unchanged", got, in)
	}
	got := WithAdminSkillsOff(in, false)
	for _, name := range append([]string{"wick-notes"}, adminSkills...) {
		if !slices.Contains(got, name) {
			t.Errorf("without Bash: %q missing from %v", name, got)
		}
	}
	if len(in) != 1 {
		t.Errorf("input slice was modified: %v", in)
	}
	for _, name := range adminSkills {
		if !IsBuiltinName(name) {
			t.Errorf("admin skill %q does not ship with wick", name)
		}
	}
}

func TestWithWarningHeaderKeepsFrontmatterFirst(t *testing.T) {
	got := string(withWarningHeader([]byte("---\nname: x\ndescription: y\n---\nbody\n")))
	if !strings.HasPrefix(got, "---\nname: x\ndescription: y\n---\n<!-- ") {
		t.Errorf("header not right after frontmatter:\n%s", got)
	}
	if bare := string(withWarningHeader([]byte("body\n"))); !strings.HasPrefix(bare, builtinWarningHeader) {
		t.Errorf("no-frontmatter body should get header on top:\n%s", bare)
	}
}
