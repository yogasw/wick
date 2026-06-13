package agents

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)


// canMutateSkill returns true when the caller is an admin or owns the named skill.
// It writes a 401/403 response and returns false when access is denied.
func canMutateSkill(c *tool.Ctx, name string) bool {
	u := login.GetUser(c.Context())
	if u == nil {
		c.Error(http.StatusUnauthorized, "unauthorized")
		return false
	}
	if u.IsAdmin() {
		return true
	}
	if globalTagsSvc == nil {
		c.Error(http.StatusForbidden, "forbidden")
		return false
	}
	owns, err := globalTagsSvc.UserOwnsResource(c.Context(), u.ID, name)
	if err != nil || !owns {
		c.Error(http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

func skillsPage(c *tool.Ctx) {
	files, dirs, _ := skillsync.Status()
	c.HTML(view.SkillsPage(buildSkillsPageVM(c, dirs, files, "", "")))
}

func skillsSync(c *tool.Ctx) {
	if u := login.GetUser(c.Context()); u != nil && !u.IsAdmin() {
		c.Error(http.StatusForbidden, "admins only")
		return
	}
	res, err := skillsync.Sync()
	flash := ""
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else {
		flash = fmt.Sprintf("Synced %d file(s), %d already up to date (hidden dirs skipped).", res.Copied, res.Skipped)
		if len(res.Errors) > 0 {
			flash += fmt.Sprintf(" %d error(s): %s", len(res.Errors), strings.Join(res.Errors, "; "))
		}
	}
	files, dirs, _ := skillsync.Status()
	c.HTML(view.SkillsPage(buildSkillsPageVM(c, dirs, files, flash, errMsg)))
}

func skillsUpload(c *tool.Ctx) {
	if u := login.GetUser(c.Context()); u != nil && !u.IsAdmin() {
		c.Error(http.StatusForbidden, "admins only")
		return
	}
	if err := c.R.ParseMultipartForm(10 << 20); err != nil {
		c.Error(http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	f, header, err := c.R.FormFile("file")
	if err != nil {
		c.Error(http.StatusBadRequest, "file required")
		return
	}
	defer f.Close()

	filename := filepath.Base(header.Filename)
	if filename == "" || filename == "." {
		c.Error(http.StatusBadRequest, "invalid filename")
		return
	}

	data, err := io.ReadAll(f)
	if err != nil {
		c.Error(http.StatusInternalServerError, "read file: "+err.Error())
		return
	}

	folderName, res, err := skillsync.UploadProcessed(filename, data)
	flash := ""
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else if res.Copied == 0 {
		errMsg = fmt.Sprintf("nothing imported from %q — check the archive structure", filename)
		if len(res.Errors) > 0 {
			errMsg += fmt.Sprintf(" (%s)", strings.Join(res.Errors, "; "))
		}
	} else {
		flash = fmt.Sprintf("Uploaded %q as skill %q to %d dir(s).", filename, folderName, res.Copied)
		if len(res.Errors) > 0 {
			flash += fmt.Sprintf(" %d error(s): %s", len(res.Errors), strings.Join(res.Errors, "; "))
		}
		if globalTagsSvc != nil && folderName != "" {
			_ = globalTagsSvc.CreateResourceOwnerTag(c.Context(), folderName, actorID(c))
		}
		if globalSkillStore != nil && folderName != "" {
			_ = globalSkillStore.Register(c.Context(), folderName, actorID(c), "")
		}
	}
	files, dirs, _ := skillsync.Status()
	c.HTML(view.SkillsPage(buildSkillsPageVM(c, dirs, files, flash, errMsg)))
}

func skillDetail(c *tool.Ctx) {
	skillDetailByPath(c, c.PathValue("name"))
}

// skillEntrySync force-copies a skill entry from dirs that have it to dirs that don't.
// For files that exist in multiple dirs, newest mtime wins.
func skillEntrySync(c *tool.Ctx) {
	name := c.PathValue("name")
	if !canMutateSkill(c, name) {
		return
	}
	allDirs := skillsync.KnownDirs()

	// find source: first dir that has this entry
	var srcDir string
	var srcNewest time.Time
	for _, d := range allDirs {
		fi, err := os.Stat(filepath.Join(d, name))
		if err != nil {
			continue
		}
		if srcDir == "" || fi.ModTime().After(srcNewest) {
			srcDir = d
			srcNewest = fi.ModTime()
		}
	}
	if srcDir == "" {
		c.Redirect(c.Base()+"/skills/"+name, http.StatusSeeOther)
		return
	}

	src := filepath.Join(srcDir, name)
	_ = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		for _, dst := range allDirs {
			if dst == srcDir {
				continue
			}
			dstPath := filepath.Join(dst, rel)
			if info.IsDir() {
				_ = os.MkdirAll(dstPath, 0o755)
				continue
			}
			_ = os.MkdirAll(filepath.Dir(dstPath), 0o755)
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				continue
			}
			_ = os.WriteFile(dstPath, data, 0o644)
			_ = os.Chtimes(dstPath, info.ModTime(), info.ModTime())
		}
		return nil
	})
	c.Redirect(c.Base()+"/skills/"+name, http.StatusSeeOther)
}

func skillDetailByPath(c *tool.Ctx, name string) {
	entries, presentIn, _ := skillsync.ListDir(name)
	if len(presentIn) > 0 {
		// It's a folder — render folder explorer
		_, allDirs, _ := skillsync.Status()
		missing := dirsNotIn(allDirs, presentIn)
		vm := view.SkillFolderVM{
			Layout:     sidebarVM(c, "skills", ""),
			Base:       c.Base(),
			FolderName: name,
			InDirs:     presentIn,
			Missing:    missing,
		}
		for _, e := range entries {
			vm.Entries = append(vm.Entries, view.SkillFileVM{
				Name:    e.Name,
				IsDir:   e.IsDir,
				InDirs:  e.Sources,
				Missing: e.Missing,
			})
		}
		c.HTML(view.SkillFolderPage(vm))
		return
	}

	// File viewer
	data, srcPath, err := skillsync.ReadFile(name)
	if err != nil {
		c.NotFound()
		return
	}
	_, dirs, _ := skillsync.Status()
	inDirs := dirsContaining(dirs, name)
	c.HTML(view.SkillDetailPage(view.SkillDetailVM{
		Layout:     sidebarVM(c, "skills", ""),
		Base:       c.Base(),
		Filename:   name,
		Content:    string(data),
		SourcePath: srcPath,
		InDirs:     inDirs,
	}))
}

func skillDelete(c *tool.Ctx) {
	name := c.PathValue("name")
	if !canMutateSkill(c, name) {
		return
	}
	skillsync.DeleteEntry(name)
	if globalTagsSvc != nil {
		_ = globalTagsSvc.DeleteResourceOwnerTag(c.Context(), name)
	}
	c.Redirect(c.Base()+"/skills", http.StatusSeeOther)
}

func skillDownload(c *tool.Ctx) {
	name := c.PathValue("name")
	data, err := skillsync.ZipEntry(name)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	c.W.Header().Set("Content-Type", "application/zip")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+name+`.zip"`)
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(data)
}

func skillDeleteFromDir(c *tool.Ctx) {
	name := c.PathValue("name")
	if !canMutateSkill(c, name) {
		return
	}
	dirLabel := c.PathValue("dirLabel")
	var targetDir string
	for _, d := range skillsync.KnownDirs() {
		if skillsync.DirLabel(d) == dirLabel {
			targetDir = d
			break
		}
	}
	if targetDir == "" {
		c.Error(http.StatusBadRequest, "unknown dir label: "+dirLabel)
		return
	}
	if err := skillsync.DeleteEntryFromDir(targetDir, name); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	// Redirect back to folder page if it still exists somewhere, else to list
	_, presentIn, _ := skillsync.ListDir(name)
	if len(presentIn) > 0 {
		c.Redirect(c.Base()+"/skills/"+name, http.StatusSeeOther)
	} else {
		if globalTagsSvc != nil {
			_ = globalTagsSvc.DeleteResourceOwnerTag(c.Context(), name)
		}
		c.Redirect(c.Base()+"/skills", http.StatusSeeOther)
	}
}

func skillFolderFileDetail(c *tool.Ctx) {
	folder := c.PathValue("folder")
	file := c.PathValue("file")
	name := folder + "/" + file
	data, srcPath, err := skillsync.ReadFile(name)
	if err != nil {
		c.NotFound()
		return
	}
	_, dirs, _ := skillsync.Status()
	inDirs := dirsContaining(dirs, name)
	c.HTML(view.SkillDetailPage(view.SkillDetailVM{
		Layout:     sidebarVM(c, "skills", ""),
		Base:       c.Base(),
		Filename:   name,
		Content:    string(data),
		SourcePath: srcPath,
		InDirs:     inDirs,
	}))
}

func skillFolderFileDownload(c *tool.Ctx) {
	folder := c.PathValue("folder")
	file := c.PathValue("file")
	name := folder + "/" + file
	data, err := skillsync.ZipEntry(name)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	c.W.Header().Set("Content-Type", "application/zip")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+file+`.zip"`)
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(data)
}

func skillFolderFileDelete(c *tool.Ctx) {
	folder := c.PathValue("folder")
	file := c.PathValue("file")
	name := folder + "/" + file
	if !canMutateSkill(c, folder) {
		return
	}
	skillsync.DeleteEntry(name)
	c.Redirect(c.Base()+"/skills/"+folder, http.StatusSeeOther)
}

// safeSkillPath rejects any path containing ".." segments.
func safeSkillPath(p string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(p))
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." || seg == "." {
			return "", false
		}
	}
	return filepath.FromSlash(clean), true
}

// resolveProviderDir returns the dir for a provider label, or "".
func resolveProviderDir(provider string) string {
	for _, d := range skillsync.KnownDirs() {
		if skillsync.DirLabel(d) == provider {
			return d
		}
	}
	return ""
}

// skillProviderPath handles GET /skills/{provider}/{path...}
// If the resolved path is a directory → render folder page.
// If it's a file → render file detail page.
func skillProviderPath(c *tool.Ctx) {
	provider := c.PathValue("provider")
	rawPath := c.PathValue("path")

	cleanPath, ok := safeSkillPath(rawPath)
	if !ok {
		c.Error(http.StatusBadRequest, "invalid path")
		return
	}

	providerDir := resolveProviderDir(provider)
	if providerDir == "" {
		// Not a provider label — treat provider+path as a skillsync subfolder path.
		fullPath := provider
		if cleanPath != "" {
			fullPath = provider + "/" + cleanPath
		}
		skillDetailByPath(c, fullPath)
		return
	}

	target := filepath.Join(providerDir, cleanPath)
	fi, err := os.Stat(target)
	if err != nil {
		c.NotFound()
		return
	}

	allDirs := skillsync.KnownDirs()
	allProviders := make([]string, 0, len(allDirs))
	for _, d := range allDirs {
		allProviders = append(allProviders, skillsync.DirLabel(d))
	}

	if fi.IsDir() {
		entries, _ := os.ReadDir(target)
		vm := view.SkillProviderFolderVM{
			Layout:       sidebarVM(c, "skills", ""),
			Base:         c.Base(),
			Provider:     provider,
			FolderName:   cleanPath,
			AllProviders: allProviders,
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			vm.Entries = append(vm.Entries, view.SkillFileVM{
				Name:  e.Name(),
				IsDir: e.IsDir(),
			})
		}
		c.HTML(view.SkillProviderFolderPage(vm))
		return
	}

	// File
	data, err := os.ReadFile(target)
	if err != nil {
		c.NotFound()
		return
	}
	hasFile := make(map[string]bool)
	for _, d := range allDirs {
		lbl := skillsync.DirLabel(d)
		if _, err := os.Stat(filepath.Join(d, cleanPath)); err == nil {
			hasFile[lbl] = true
		}
	}
	// folder part = all but last segment
	parts := strings.Split(filepath.ToSlash(cleanPath), "/")
	folderPart := strings.Join(parts[:len(parts)-1], "/")
	filename := parts[len(parts)-1]

	c.HTML(view.SkillProviderFilePage(view.SkillProviderFileVM{
		Layout:       sidebarVM(c, "skills", ""),
		Base:         c.Base(),
		Provider:     provider,
		FolderName:   folderPart,
		Filename:     filename,
		Content:      string(data),
		SourcePath:   target,
		AllProviders: allProviders,
		HasFile:      hasFile,
	}))
}

// skillProviderSync handles POST /skills-sync/{provider}/{path...}
// Copies the entry (file or folder) from the given provider to all others.
func skillProviderSync(c *tool.Ctx) {
	provider := c.PathValue("provider")
	rawPath := c.PathValue("path")

	cleanPath, ok := safeSkillPath(rawPath)
	if !ok {
		c.Error(http.StatusBadRequest, "invalid path")
		return
	}

	skillName := rawPath
	if skillName == "" {
		skillName = provider
	}
	if !canMutateSkill(c, skillName) {
		return
	}

	srcDir := resolveProviderDir(provider)
	if srcDir == "" {
		c.Error(http.StatusBadRequest, "unknown provider: "+provider)
		return
	}

	src := filepath.Join(srcDir, cleanPath)
	allDirs := skillsync.KnownDirs()

	_ = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		for _, dst := range allDirs {
			if dst == srcDir {
				continue
			}
			dstPath := filepath.Join(dst, rel)
			if info.IsDir() {
				_ = os.MkdirAll(dstPath, 0o755)
				continue
			}
			_ = os.MkdirAll(filepath.Dir(dstPath), 0o755)
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				continue
			}
			_ = os.WriteFile(dstPath, data, 0o644)
			_ = os.Chtimes(dstPath, info.ModTime(), info.ModTime())
		}
		return nil
	})

	c.Redirect(c.Base()+"/skills/"+provider+"/"+filepath.ToSlash(cleanPath), http.StatusSeeOther)
}

func buildSkillsPageVM(c *tool.Ctx, dirs []string, files []skillsync.SkillFile, flash, errMsg string) view.SkillsPageVM {
	vm := view.SkillsPageVM{
		Layout: sidebarVM(c, "skills", ""),
		Base:   c.Base(),
		Dirs:   dirs,
		Flash:  flash,
		Error:  errMsg,
	}
	for _, f := range files {
		vm.Files = append(vm.Files, view.SkillFileVM{
			Name:    f.Name,
			IsDir:   f.IsDir,
			InDirs:  f.Sources,
			Missing: f.Missing,
		})
	}
	return vm
}

func dirsContaining(dirs []string, filename string) []string {
	var out []string
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, filename)); err == nil {
			out = append(out, d)
		}
	}
	return out
}

func dirsNotIn(all, present []string) []string {
	set := make(map[string]bool, len(present))
	for _, d := range present {
		set[d] = true
	}
	var out []string
	for _, d := range all {
		if !set[d] {
			out = append(out, d)
		}
	}
	return out
}
