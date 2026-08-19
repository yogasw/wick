package skillsync

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// catalog.go renders the list of skills that ship inside the wick binary as a
// system-prompt block for the CLI providers (claude, codex).
//
// Those CLIs discover skills from their OWN config dir, and wick's shipped
// skills deliberately never get copied there (see builtin.go): one copy, one
// owner. That keeps the files safe to rewrite, but it also means the CLI's own
// loader will never surface them — so wick has to name them itself.
//
// Naming them is only half the job. The block gives an absolute path per skill
// because the agent opens the SKILL.md with its own read tool, and that read is
// refused unless the directory is also trusted on the argv. Each provider's
// skilldir.go --add-dir's the same directory for exactly that reason; changing
// one without the other leaves the agent reading about files it cannot open.
//
// Only name + one-line description + path go in. The bodies stay on disk and
// are read on demand, so a growing library costs the prompt a line apiece.

// builtinCatalogMaxBytes bounds the injected block so a large shipped library
// cannot crowd out the operator's own preset.
const builtinCatalogMaxBytes = 4 * 1024

// builtinDescMaxRunes caps each description. Trimmed PER SKILL rather than by
// dropping skills off the end: a skill the model cannot see is unusable, while
// a shortened description still routes it to the right SKILL.md — which it
// reads in full anyway.
const builtinDescMaxRunes = 160

// BuiltinCatalog returns a markdown block naming every skill that ships with
// wick, or "" when none are installed on disk.
//
// Sorted by name so the rendered prompt is byte-stable across spawns, which
// keeps the provider's prompt prefix cacheable.
func BuiltinCatalog() string {
	dir := BuiltinDir()
	if dir == "" {
		return ""
	}
	// ReadDirs (not KnownDirs) so the extraction-on-first-use path runs: a
	// spawn can be the first thing that ever touches the shipped skills.
	_ = ReadDirs()

	names := make([]string, 0, len(BuiltinNames()))
	for name := range BuiltinNames() {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Built-in wick skills\n\n")
	b.WriteString("These skills ship with wick and explain how to work with it. " +
		"Each is listed with the full path to its SKILL.md. " +
		"To use one, read that file first and then follow it — do not guess its steps.\n")
	b.WriteString("They are rewritten from the wick binary on every start: never edit or delete one.\n\n")

	wrote := 0
	for i, name := range names {
		meta := resolveMetaForEntry(name, []string{dir})
		if meta == nil {
			continue // not extracted yet, or unreadable — nothing truthful to say
		}
		label := name
		if n := strings.TrimSpace(meta["name"]); n != "" {
			label = n
		}
		desc := truncateRunes(oneLine(meta["description"]), builtinDescMaxRunes)

		var line strings.Builder
		line.WriteString("- **")
		line.WriteString(label)
		line.WriteString("**")
		if desc != "" {
			line.WriteString(" — ")
			line.WriteString(desc)
		}
		// filepath.Join, not a "/" literal: the agent passes this straight to
		// its read tool, and on Windows a forward-slash path does not match the
		// --add-dir root the same string was used to authorise.
		line.WriteString("  (")
		line.WriteString(filepath.Join(dir, name, "SKILL.md"))
		line.WriteString(")\n")

		if b.Len()+line.Len() > builtinCatalogMaxBytes {
			// Name the count: a silently shrinking catalog would read as a
			// smaller library rather than a truncated one.
			fmt.Fprintf(&b, "- …(%d more omitted — catalog truncated)\n", len(names)-i)
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

// AppendBuiltinCatalog returns preset with the shipped-skill catalog appended,
// or preset unchanged when nothing ships.
//
// Appended rather than prepended: the operator's preset opens with the session
// identity block, and a catalog above it would push that out of the position
// the CLIs and the operator both expect to find it in.
//
// An empty preset still gets the catalog. A spawn with no preset is a bare
// agent, and a bare agent is exactly the one that most needs to be told the
// shipped skills exist.
func AppendBuiltinCatalog(preset string) string {
	cat := BuiltinCatalog()
	if cat == "" {
		return preset
	}
	if strings.TrimSpace(preset) == "" {
		return cat
	}
	return strings.TrimRight(preset, "\n") + "\n\n" + cat
}

// oneLine flattens whitespace so a multi-line description stays one bullet.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// truncateRunes shortens s to at most n runes, appending "…" when cut.
// Rune-based so a multi-byte character is never split mid-sequence.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}
