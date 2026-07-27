package wick

import (
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/skillsync"
)

// skillcatalog.go injects a COMPACT catalog of available skills into the
// wick agent's system prompt. Unlike the CLI providers (which get a trusted
// `--add-dir ~/.<provider>/skills` and let their own loader surface skills),
// wick runs in-process and must tell the model itself. We inject only
// name + one-line description + the path to each SKILL.md — never the full
// body — so the prompt stays small; the agent reads the SKILL.md on demand
// with read_file when it actually engages a skill (the read roots include
// the skill dirs, see tool_fs.go).

// skillCatalogMaxBytes bounds the injected block so a huge skill library
// can't blow the system-prompt budget. Matches contextFileMaxBytes intent.
const skillCatalogMaxBytes = 8 * 1024

// skillCatalog returns a markdown catalog block for the system prompt, or ""
// when there are no skills. Each line: name — description  (path/SKILL.md).
// Prefers the wick copy's path so the read target is one the agent can reach.
func skillCatalog() string {
	skills := skillsync.ListSkills()
	if len(skills) == 0 {
		return ""
	}
	// Stable order: name asc, so the prompt prefix stays cacheable.
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })

	var b strings.Builder
	b.WriteString("## Available skills\n\n")
	b.WriteString("Each skill is a folder with a SKILL.md. To use one, read its SKILL.md with " +
		"read_file, then follow it. Do NOT guess a skill's steps — read the file first.\n\n")

	wrote := 0
	for _, s := range skills {
		name := s.Name
		if n := strings.TrimSpace(s.Meta["name"]); n != "" {
			name = n
		}
		desc := strings.TrimSpace(s.Meta["description"])
		path := skillMdPath(s)
		var line strings.Builder
		line.WriteString("- **")
		line.WriteString(name)
		line.WriteString("**")
		if desc != "" {
			line.WriteString(" — ")
			line.WriteString(oneLine(desc))
		}
		if path != "" {
			line.WriteString("  (")
			line.WriteString(path)
			line.WriteString(")")
		}
		line.WriteString("\n")
		if b.Len()+line.Len() > skillCatalogMaxBytes {
			b.WriteString("- …(more skills omitted — catalog truncated)\n")
			break
		}
		b.WriteString(line.String())
		wrote++
	}
	if wrote == 0 {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

// skillMdPath returns the on-disk path to a skill's SKILL.md the agent should
// read: the wick-dir copy when present (a path the read roots always allow),
// else the first provider that has the skill. Only meaningful for folder
// skills; a bare-file skill returns its own path.
func skillMdPath(s skillsync.SkillInfo) string {
	pick := func(loc skillsync.ProviderLocation) string {
		if s.IsDir {
			return loc.Path + "/SKILL.md"
		}
		return loc.Path
	}
	for _, loc := range s.InProviders {
		if loc.Label == "wick" {
			return pick(loc)
		}
	}
	if len(s.InProviders) > 0 {
		return pick(s.InProviders[0])
	}
	return ""
}

// oneLine flattens whitespace/newlines so a multi-line description doesn't
// break the single-bullet format.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
