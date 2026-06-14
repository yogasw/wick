package agents

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// SkillListItem is one row in GET /api/skills.
type SkillListItem struct {
	Name        string   `json:"name"`
	IsDir       bool     `json:"is_dir"`
	InDirs      []string `json:"in_dirs"`
	MissingDirs []string `json:"missing_dirs"`
}

// SkillListResponse is the envelope for GET /api/skills.
type SkillListResponse struct {
	Dirs   []string        `json:"dirs"`
	Skills []SkillListItem `json:"skills"`
}

// SkillDetailResponse is the envelope for GET /api/skills/{name}.
type SkillDetailResponse struct {
	Name        string          `json:"name"`
	IsDir       bool            `json:"is_dir"`
	Content     string          `json:"content,omitempty"`
	SourcePath  string          `json:"source_path,omitempty"`
	InDirs      []string        `json:"in_dirs"`
	Entries     []SkillListItem `json:"entries,omitempty"`
	MissingDirs []string        `json:"missing_dirs,omitempty"`
}

// SkillFileDetailResponse is the envelope for GET /api/skills/{folder}/files/{file...}.
type SkillFileDetailResponse struct {
	Name       string          `json:"name"`
	IsDir      bool            `json:"is_dir"`
	Content    string          `json:"content,omitempty"`
	SourcePath string          `json:"source_path,omitempty"`
	InDirs     []string        `json:"in_dirs"`
	Entries    []SkillListItem `json:"entries,omitempty"`
}

// SkillProviderEntryResponse is the envelope for GET /api/skills/{provider}/{path...}.
type SkillProviderEntryResponse struct {
	Provider     string          `json:"provider"`
	Path         string          `json:"path"`
	IsDir        bool            `json:"is_dir"`
	Content      string          `json:"content,omitempty"`
	SourcePath   string          `json:"source_path,omitempty"`
	Entries      []SkillListItem `json:"entries,omitempty"`
	AllProviders []string        `json:"all_providers"`
	HasFile      map[string]bool `json:"has_file,omitempty"`
}

/* ── helper ──────────────────────────────────────────────────────────────── */

// buildSkillListItems converts skillsync.SkillFile entries into SkillListItem DTOs.
func buildSkillListItems(files []skillsync.SkillFile) []SkillListItem {
	items := make([]SkillListItem, 0, len(files))
	for _, f := range files {
		missing := f.Missing
		if missing == nil {
			missing = []string{}
		}
		sources := f.Sources
		if sources == nil {
			sources = []string{}
		}
		items = append(items, SkillListItem{
			Name:        f.Name,
			IsDir:       f.IsDir,
			InDirs:      sources,
			MissingDirs: missing,
		})
	}
	return items
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// apiSkillsList handles GET /api/skills and returns all known skills + dirs.
func apiSkillsList(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	files, dirs, _ := skillsync.Status()
	if dirs == nil {
		dirs = []string{}
	}
	c.JSON(http.StatusOK, SkillListResponse{
		Dirs:   dirs,
		Skills: buildSkillListItems(files),
	})
}

// apiSkillDetail handles GET /api/skills/{name}.
// When {name} resolves to a folder, entries are returned.
// When it resolves to a file, content + source path are returned.
func apiSkillDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := c.PathValue("name")

	entries, presentIn, _ := skillsync.ListDir(name)
	if len(presentIn) > 0 {
		_, allDirs, _ := skillsync.Status()
		missing := dirsNotIn(allDirs, presentIn)
		if missing == nil {
			missing = []string{}
		}
		resp := SkillDetailResponse{
			Name:        name,
			IsDir:       true,
			InDirs:      presentIn,
			MissingDirs: missing,
			Entries:     buildSkillListItems(skillEntriesToFiles(entries)),
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	data, srcPath, err := skillsync.ReadFile(name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "skill not found"})
		return
	}
	_, dirs, _ := skillsync.Status()
	inDirs := dirsContaining(dirs, name)
	if inDirs == nil {
		inDirs = []string{}
	}
	c.JSON(http.StatusOK, SkillDetailResponse{
		Name:       name,
		IsDir:      false,
		Content:    string(data),
		SourcePath: srcPath,
		InDirs:     inDirs,
	})
}

// apiSkillFolderFileDetail handles GET /api/skills/{folder}/files/{file...}.
// Supports nested paths and returns a directory listing when the path resolves to a directory.
func apiSkillFolderFileDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	folder := c.PathValue("folder")
	file := c.PathValue("file")

	cleanFile, ok := safeSkillPath(file)
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid path"})
		return
	}
	name := folder + "/" + cleanFile

	dirs := skillsync.KnownDirs()
	for _, d := range dirs {
		target := filepath.Join(d, name)
		fi, err := os.Stat(target)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			entries, _ := os.ReadDir(target)
			items := make([]SkillListItem, 0, len(entries))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				items = append(items, SkillListItem{
					Name:        e.Name(),
					IsDir:       e.IsDir(),
					InDirs:      []string{d},
					MissingDirs: []string{},
				})
			}
			_, allDirs, _ := skillsync.Status()
			inDirs := dirsContaining(allDirs, name)
			if inDirs == nil {
				inDirs = []string{}
			}
			c.JSON(http.StatusOK, SkillFileDetailResponse{
				Name:    name,
				IsDir:   true,
				InDirs:  inDirs,
				Entries: items,
			})
			return
		}
		break
	}

	data, srcPath, err := skillsync.ReadFile(name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	_, allDirs, _ := skillsync.Status()
	inDirs := dirsContaining(allDirs, name)
	if inDirs == nil {
		inDirs = []string{}
	}
	c.JSON(http.StatusOK, SkillFileDetailResponse{
		Name:       name,
		IsDir:      false,
		Content:    string(data),
		SourcePath: srcPath,
		InDirs:     inDirs,
	})
}

// apiSkillProviderPath handles GET /api/skills/{provider}/{path...}.
// When the provider label is known, it serves the raw provider directory tree.
// When unknown, it falls back to the shared skillsync view (same as apiSkillDetail).
func apiSkillProviderPath(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	provider := c.PathValue("provider")
	rawPath := c.PathValue("path")

	cleanPath, ok := safeSkillPath(rawPath)
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid path"})
		return
	}

	providerDir := resolveProviderDir(provider)
	if providerDir == "" {
		fullPath := provider
		if cleanPath != "" {
			fullPath = provider + "/" + cleanPath
		}
		apiSkillDetailByName(c, fullPath)
		return
	}

	target := filepath.Join(providerDir, cleanPath)
	fi, err := os.Stat(target)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "path not found"})
		return
	}

	allDirs := skillsync.KnownDirs()
	allProviders := make([]string, 0, len(allDirs))
	for _, d := range allDirs {
		allProviders = append(allProviders, skillsync.DirLabel(d))
	}

	if fi.IsDir() {
		dirEntries, _ := os.ReadDir(target)
		items := make([]SkillListItem, 0, len(dirEntries))
		for _, e := range dirEntries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			items = append(items, SkillListItem{
				Name:        e.Name(),
				IsDir:       e.IsDir(),
				InDirs:      []string{provider},
				MissingDirs: []string{},
			})
		}
		c.JSON(http.StatusOK, SkillProviderEntryResponse{
			Provider:     provider,
			Path:         cleanPath,
			IsDir:        true,
			Entries:      items,
			AllProviders: allProviders,
		})
		return
	}

	data, err := os.ReadFile(target)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	hasFile := make(map[string]bool)
	for _, d := range allDirs {
		lbl := skillsync.DirLabel(d)
		if _, err := os.Stat(filepath.Join(d, cleanPath)); err == nil {
			hasFile[lbl] = true
		}
	}
	c.JSON(http.StatusOK, SkillProviderEntryResponse{
		Provider:     provider,
		Path:         cleanPath,
		IsDir:        false,
		Content:      string(data),
		SourcePath:   target,
		AllProviders: allProviders,
		HasFile:      hasFile,
	})
}

// apiSkillDetailByName is the shared fallback used by apiSkillProviderPath when
// the provider segment is not a recognised dir label.
func apiSkillDetailByName(c *tool.Ctx, name string) {
	entries, presentIn, _ := skillsync.ListDir(name)
	if len(presentIn) > 0 {
		_, allDirs, _ := skillsync.Status()
		missing := dirsNotIn(allDirs, presentIn)
		if missing == nil {
			missing = []string{}
		}
		c.JSON(http.StatusOK, SkillDetailResponse{
			Name:        name,
			IsDir:       true,
			InDirs:      presentIn,
			MissingDirs: missing,
			Entries:     buildSkillListItems(skillEntriesToFiles(entries)),
		})
		return
	}
	data, srcPath, err := skillsync.ReadFile(name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "skill not found"})
		return
	}
	_, dirs, _ := skillsync.Status()
	inDirs := dirsContaining(dirs, name)
	if inDirs == nil {
		inDirs = []string{}
	}
	c.JSON(http.StatusOK, SkillDetailResponse{
		Name:       name,
		IsDir:      false,
		Content:    string(data),
		SourcePath: srcPath,
		InDirs:     inDirs,
	})
}

// skillEntriesToFiles converts []skillsync.SkillEntry → []skillsync.SkillFile (same type).
func skillEntriesToFiles(entries []skillsync.SkillEntry) []skillsync.SkillFile {
	out := make([]skillsync.SkillFile, len(entries))
	for i, e := range entries {
		out[i] = skillsync.SkillFile{
			Name:    e.Name,
			IsDir:   e.IsDir,
			Sources: e.Sources,
			Missing: e.Missing,
			Newest:  e.Newest,
		}
	}
	return out
}
