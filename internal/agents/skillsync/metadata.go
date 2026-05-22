package skillsync

import (
	"bytes"
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
type SkillInfo struct {
	Name             string            `json:"name"`
	IsDir            bool              `json:"is_dir"`
	Meta             map[string]string `json:"meta"`
	InProviders      []ProviderLocation `json:"in_providers"`
	MissingProviders []ProviderLocation `json:"missing_providers"`
}

// metaFilenames is the ordered list of candidate files to read frontmatter from
// inside a skill folder. First match wins.
var metaFilenames = []string{"SKILL.md", "SKILL.txt", "TOOL.md", "TOOL.txt", "README.md"}

// ListSkills returns enriched SkillInfo for every top-level entry across all
// known skill dirs, including parsed metadata and per-provider paths.
func ListSkills() []SkillInfo {
	dirs := KnownDirs()
	files := scan(dirs)

	out := make([]SkillInfo, 0, len(files))
	for _, f := range files {
		meta := resolveMetaForEntry(f.Name, dirs)

		inProviders := make([]ProviderLocation, 0, len(f.Sources))
		for _, d := range f.Sources {
			inProviders = append(inProviders, ProviderLocation{
				Label: DirLabel(d),
				Dir:   d,
				Path:  filepath.Join(d, f.Name),
			})
		}
		missingProviders := make([]ProviderLocation, 0, len(f.Missing))
		for _, d := range f.Missing {
			missingProviders = append(missingProviders, ProviderLocation{
				Label: DirLabel(d),
				Dir:   d,
				Path:  filepath.Join(d, f.Name),
			})
		}

		out = append(out, SkillInfo{
			Name:             f.Name,
			IsDir:            f.IsDir,
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
func resolveMetaForEntry(name string, dirs []string) map[string]string {
	for _, d := range dirs {
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

// parseFrontmatter extracts YAML-style frontmatter delimited by "---" lines.
// Only reads simple key: value pairs — no YAML library needed for this subset.
func parseFrontmatter(data []byte) map[string]string {
	s := strings.TrimSpace(string(data))
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
	for _, line := range bytes.Split([]byte(block), []byte("\n")) {
		k, v, ok := strings.Cut(string(line), ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		meta[k] = v
	}
	return meta
}
