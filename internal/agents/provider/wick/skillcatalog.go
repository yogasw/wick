package wick

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/internal/appname"
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

// skillDescMaxRunes caps each skill's description. Descriptions are trimmed
// PER SKILL rather than dropping skills off the end of the list: a skill the
// model cannot see is unusable, while a shortened description still routes it
// to the right SKILL.md (which it reads in full anyway). Some skills in this
// repo carry ~1.5KB descriptions, so without this a handful of them consumed
// the whole budget and silently truncated the alphabetical tail.
const skillDescMaxRunes = 160

// builtinTag marks skills that ship inside the wick binary. The agent needs to
// know: those files are rewritten from scratch on every start, so suggesting an
// edit to one would be advice that silently gets undone.
const builtinTag = "[built-in]"

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
		"read_file, then follow it. Do NOT guess a skill's steps — read the file first.\n")
	b.WriteString("Skills tagged " + builtinTag + " ship with wick and are rewritten on every " +
		"start — never offer to edit or delete one.\n\n")

	// Resolve once: the label is fixed for the process lifetime.
	ownLabel := appname.Resolve()

	wrote := 0
	for _, s := range skills {
		name := s.Name
		if n := strings.TrimSpace(s.Meta["name"]); n != "" {
			name = n
		}
		desc := truncateRunes(oneLine(s.Meta["description"]), skillDescMaxRunes)
		path := skillMdPath(s, ownLabel)
		var line strings.Builder
		line.WriteString("- **")
		line.WriteString(name)
		line.WriteString("**")
		if s.Builtin {
			line.WriteString(" ")
			line.WriteString(builtinTag)
		}
		if desc != "" {
			line.WriteString(" — ")
			line.WriteString(desc)
		}
		if path != "" {
			line.WriteString("  (")
			line.WriteString(path)
			line.WriteString(")")
		}
		line.WriteString("\n")
		if b.Len()+line.Len() > skillCatalogMaxBytes {
			// Name the count so a silently shrinking catalog is visible in the
			// prompt rather than looking like the library is simply smaller.
			fmt.Fprintf(&b, "- …(%d more skills omitted — catalog truncated)\n", len(skills)-wrote)
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
// read: the copy under wick's own dir when present (a path the read roots
// always allow), else the first provider that has the skill. Only meaningful
// for folder skills; a bare-file skill returns its own path.
//
// ownLabel is wick's own dir label, which is the RESOLVED APP NAME — "wick" for
// a release build but "wick-lab" (or whatever wick.yml names) for a dev one.
// It is passed in rather than hardcoded: matching the literal "wick" made every
// dev build miss its own copy and fall through to another provider's path.
func skillMdPath(s skillsync.SkillInfo, ownLabel string) string {
	pick := func(loc skillsync.ProviderLocation) string {
		if s.IsDir {
			return loc.Path + "/SKILL.md"
		}
		return loc.Path
	}
	for _, loc := range s.InProviders {
		if loc.Label == ownLabel {
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

// truncateRunes shortens s to at most n runes, appending "…" when cut.
// Rune-based so a multi-byte character is never split mid-sequence.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}
