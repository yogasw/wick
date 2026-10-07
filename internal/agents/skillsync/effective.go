package skillsync

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill sources an EffectiveSkill comes from.
const (
	SourceLocal   = "local"
	SourceGlobal  = "global"
	SourceBuiltin = "builtin"
)

// EffectiveSkill is one skill a Team agent's spawn can see, as its
// Settings › Skills tab lists it.
type EffectiveSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	// Overrides is the source a local skill hides under the same name
	// ("local wins"); Shadowed marks that hidden entry.
	Overrides string `json:"overrides,omitempty"`
	Shadowed  bool   `json:"shadowed,omitempty"`
	// Required skills cannot be switched off (IsRequiredSkill).
	Required bool `json:"required,omitempty"`
}

// LocalSkillsDir is the project-local skill folder under projectDir.
func LocalSkillsDir(projectDir string) string {
	return filepath.Join(projectDir, ".claude", "skills")
}

// EffectiveSkills lists the skills an agent working in projectDir sees:
// its project's own (localDir) first, then the global and built-in ones
// (others, as ListSkills returns them). A local skill wins over a global
// or built-in one of the same name; the hidden one stays in the list,
// marked Shadowed, so the tab can say why.
func EffectiveSkills(localDir string, others []SkillInfo) []EffectiveSkill {
	var out []EffectiveSkill
	local := map[string]bool{}
	if entries, err := os.ReadDir(localDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := e.Name()
			local[name] = true
			desc := ""
			// Read the project's own file: resolveMetaForEntry would answer
			// a built-in name from the shipped copy.
			if data, err := os.ReadFile(filepath.Join(localDir, name, "SKILL.md")); err == nil {
				desc = oneLine(parseFrontmatter([]byte(trimLeadingHTMLComment(string(data))))["description"])
			}
			out = append(out, EffectiveSkill{Name: name, Description: desc, Source: SourceLocal, Required: IsRequiredSkill(name)})
		}
	}
	for _, s := range others {
		src := SourceGlobal
		if s.Builtin {
			src = SourceBuiltin
		}
		es := EffectiveSkill{Name: s.Name, Description: oneLine(s.Meta["description"]), Source: src, Required: s.Builtin && IsRequiredSkill(s.Name)}
		if local[s.Name] {
			es.Shadowed = true
			for i := range out {
				if out[i].Name == s.Name && out[i].Source == SourceLocal {
					out[i].Overrides = src
				}
			}
		}
		out = append(out, es)
	}
	rank := map[string]int{SourceLocal: 0, SourceGlobal: 1, SourceBuiltin: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return rank[out[i].Source] < rank[out[j].Source]
	})
	return out
}
