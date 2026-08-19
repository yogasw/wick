package skillsync

import (
	"os"
	"path/filepath"
	"strings"
)

// ProviderLocation is one provider that has a skill, with its full path.
type ProviderLocation struct {
	Label string `json:"label"`
	Dir   string `json:"dir"`
	Path  string `json:"path"` // full path to the skill entry (folder or file)
}

// SkillInfo is an enriched skill entry — name, metadata, and per-provider locations.
//
// Builtin marks a skill that ships inside the wick binary. Such a skill lives in
// a directory wick rewrites from scratch on every start, so it is effectively
// read-only: callers should not offer to edit or delete it, and should expect
// local changes to disappear. The flag is derived from which directory the skill
// was found in — no manifest or database row is involved.
type SkillInfo struct {
	Name             string             `json:"name"`
	IsDir            bool               `json:"is_dir"`
	Builtin          bool               `json:"builtin"`
	Meta             map[string]string  `json:"meta"`
	InProviders      []ProviderLocation `json:"in_providers"`
	MissingProviders []ProviderLocation `json:"missing_providers"`
}

// metaFilenames is the ordered list of candidate files to read frontmatter from
// inside a skill folder. First match wins.
var metaFilenames = []string{"SKILL.md", "SKILL.txt", "TOOL.md", "TOOL.txt", "README.md"}

// ListSkills returns enriched SkillInfo for every top-level entry across all
// known skill dirs, including parsed metadata and per-provider paths.
func ListSkills() []SkillInfo {
	// ReadDirs, not KnownDirs: the built-in dir is readable but never synced.
	dirs := ReadDirs()
	files := scan(dirs)

	out := make([]SkillInfo, 0, len(files))
	for _, f := range files {
		meta := resolveMetaForEntry(f.Name, dirs)

		inProviders := make([]ProviderLocation, 0, len(f.Sources))
		// Shipped skills share a directory with the user's own now, so the flag
		// comes from the embed's entry list rather than from which dir the skill
		// was found in.
		builtin := IsBuiltinName(f.Name)
		for _, d := range f.Sources {
			inProviders = append(inProviders, ProviderLocation{
				Label: DirLabel(d),
				Dir:   d,
				Path:  filepath.Join(d, f.Name),
			})
		}
		// A shipped skill is absent from the other providers' dirs on purpose,
		// so listing them as "missing" would invite a UI to offer a sync that
		// Sync itself refuses. Nothing is missing — every provider reaches it
		// by trusting wick's dir.
		missingProviders := make([]ProviderLocation, 0, len(f.Missing))
		if !builtin {
			for _, d := range f.Missing {
				missingProviders = append(missingProviders, ProviderLocation{
					Label: DirLabel(d),
					Dir:   d,
					Path:  filepath.Join(d, f.Name),
				})
			}
		}

		out = append(out, SkillInfo{
			Name:             f.Name,
			IsDir:            f.IsDir,
			Builtin:          builtin,
			Meta:             meta,
			InProviders:      inProviders,
			MissingProviders: missingProviders,
		})
	}
	return out
}

// resolveMetaForEntry reads frontmatter from the first provider that has this entry.
// For folders: looks for metaFilenames inside the folder.
// For files: reads the file directly if it's a .md/.txt.
//
// For a skill wick ships, wick's own dir is consulted FIRST: a provider copy
// may be a stale fork from before the skill was shipped, and the shipped
// copy's metadata is the truthful one — it describes what wick actually
// ships, and it is the copy wick controls.
func resolveMetaForEntry(name string, dirs []string) map[string]string {
	ordered := dirs
	if bd := BuiltinDir(); bd != "" && IsBuiltinName(name) {
		ordered = make([]string, 0, len(dirs))
		ordered = append(ordered, bd)
		for _, d := range dirs {
			if d != bd {
				ordered = append(ordered, d)
			}
		}
	}
	for _, d := range ordered {
		entryPath := filepath.Join(d, name)
		fi, err := os.Stat(entryPath)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			for _, candidate := range metaFilenames {
				data, err := os.ReadFile(filepath.Join(entryPath, candidate))
				if err == nil {
					return parseFrontmatter(data)
				}
			}
		} else {
			ext := strings.ToLower(filepath.Ext(name))
			if ext == ".md" || ext == ".txt" {
				data, err := os.ReadFile(entryPath)
				if err == nil {
					return parseFrontmatter(data)
				}
			}
		}
	}
	return nil
}

// trimLeadingHTMLComment removes one leading `<!-- … -->` block, if present.
// Only the first is removed: frontmatter must follow it directly, so a document
// opening with several comments has no frontmatter to find anyway.
func trimLeadingHTMLComment(s string) string {
	if !strings.HasPrefix(s, "<!--") {
		return s
	}
	end := strings.Index(s, "-->")
	if end < 0 {
		return s // unterminated; leave it alone rather than eating the document
	}
	return s[end+len("-->"):]
}

// parseFrontmatter extracts YAML-style frontmatter delimited by "---" lines.
// Only reads simple key: value pairs — no YAML library needed for this subset.
//
// A leading HTML comment is skipped first: wick's shipped skills carry a
// "do not edit" banner above their frontmatter, and markdown renderers hide it,
// so requiring "---" at byte zero would leave those files with no metadata at
// all — an empty name and description everywhere the skill is listed.
func parseFrontmatter(data []byte) map[string]string {
	s := strings.TrimSpace(string(data))
	s = strings.TrimSpace(trimLeadingHTMLComment(s))
	if !strings.HasPrefix(s, "---") {
		return nil
	}
	rest := strings.TrimPrefix(s, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		end = strings.Index(rest, "---")
	}
	var block string
	if end >= 0 {
		block = rest[:end]
	} else {
		block = rest
	}

	meta := map[string]string{}
	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok {
			continue
		}
		// An indented line is a continuation, not a new key — without this a
		// wrapped value containing a colon would be parsed as its own field.
		if strings.TrimSpace(k) != strings.TrimLeft(k, " \t") {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		// Block scalars (`>`, `>-`, `|`, `|-`, …): the value is the indented
		// lines that follow. Skills routinely wrap a long description this way,
		// and reading only the text after the colon stores the literal ">-".
		if v == "" || v[0] == '>' || v[0] == '|' {
			folded := v != "" && v[0] == '>'
			var parts []string
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) == "" {
					i++
					parts = append(parts, "")
					continue
				}
				if next == strings.TrimLeft(next, " \t") {
					break // back at column zero: a new key
				}
				parts = append(parts, strings.TrimSpace(next))
				i++
			}
			if len(parts) > 0 {
				if folded {
					// Folded (`>`) joins lines with spaces; literal (`|`) keeps
					// the newlines.
					v = strings.Join(parts, " ")
				} else {
					v = strings.Join(parts, "\n")
				}
				v = strings.TrimSpace(v)
			}
		}
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		meta[k] = v
	}
	return meta
}
