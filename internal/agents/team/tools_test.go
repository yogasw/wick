package team

import (
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestDecodeNativeToolsLegacyRowKeepsEverything(t *testing.T) {
	if got := DecodeNativeTools(""); !slices.Equal(got, NativeTools) {
		t.Fatalf("legacy row = %v, want every tool", got)
	}
	if got := DecodeNativeTools(`["Write","Read","Nope"]`); !slices.Equal(got, []string{"Read", "Write"}) {
		t.Fatalf("decode = %v", got)
	}
	if got := DecodeNativeTools(`{bad`); !slices.Equal(got, DefaultNativeTools) {
		t.Fatalf("unreadable = %v, want read-only defaults", got)
	}
}

func TestDisallowedClaudeTools(t *testing.T) {
	got := DisallowedClaudeTools(DefaultNativeTools)
	want := []string{"Bash", "BashOutput", "KillShell", "Edit", "MultiEdit", "NotebookEdit", "Write"}
	if !slices.Equal(got, want) {
		t.Fatalf("default deny = %v, want %v", got, want)
	}
	if got := DisallowedClaudeTools(NativeTools); len(got) != 0 {
		t.Fatalf("all on = %v, want none", got)
	}
}

func TestValidateBashRule(t *testing.T) {
	ok := []BashRule{{Pattern: "git status"}, {Pattern: "ls *", Scope: ScopeProject}, {Pattern: "cat *", Scope: "/srv/data"}}
	for _, r := range ok {
		if err := ValidateBashRule(r); err != nil {
			t.Errorf("%+v rejected: %v", r, err)
		}
	}
	bad := []BashRule{
		{Pattern: ""}, {Pattern: "ls | sh"}, {Pattern: "ls; rm -rf /"}, {Pattern: "make && deploy"},
		{Pattern: "echo `id`"}, {Pattern: "echo $(id)"}, {Pattern: "ls", Scope: "relative/dir"},
	}
	for _, r := range bad {
		if err := ValidateBashRule(r); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
}

func TestResolveBashRulesProjectScope(t *testing.T) {
	rules := []BashRule{{Pattern: "ls *", Scope: ScopeProject}, {Pattern: "git status"}, {Pattern: "ls | sh"}}
	got := ResolveBashRules(rules, "/p/files")
	if len(got) != 2 || got[0].Scope != "/p/files" || got[1].Scope != "" {
		t.Fatalf("resolved = %+v", got)
	}
	// No project folder: a {project} rule is dropped, never left unscoped.
	if got := ResolveBashRules(rules[:1], ""); len(got) != 0 {
		t.Fatalf("unresolvable {project} kept: %+v", got)
	}
}

func TestLimitsOf(t *testing.T) {
	p := entity.AgentPersona{
		ID:                 "a1",
		AllowedNativeTools: `["Read","Bash"]`,
		BashRules:          `[{"pattern":"git status","scope":"{project}"}]`,
		DisabledSkills:     `["x"]`,
	}
	l := LimitsOf(p, "/proj")
	if l.AgentID != "a1" || !slices.Equal(l.NativeTools, []string{"Read", "Bash"}) ||
		len(l.BashRules) != 1 || l.BashRules[0].Scope != "/proj" || !slices.Equal(l.DisabledSkills, []string{"x"}) {
		t.Fatalf("limits = %+v", l)
	}
}

func TestEncodeSkillNames(t *testing.T) {
	if got := EncodeSkillNames([]string{"b", " a ", "b", ""}); got != `["a","b"]` {
		t.Fatalf("encode = %s", got)
	}
}
