// Package providerstorage mounts a Provider Storage Manager UI under /tools/provider-storage.
// It exposes file browsing, restore, upload, deletion, retention management,
// and sync-source configuration for provider credential files.
package providerstorage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/providersync"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

var globalSyncMgr *providersync.Manager
var globalConfigs *configs.Service

// SetSyncManager injects the shared Manager instance.
func SetSyncManager(m *providersync.Manager) { globalSyncMgr = m }

// Register wires provider-storage routes on the scoped Router.
func Register(r tool.Router) {
	r.GET("/", storagePage)
	r.GET("/files", listFiles)
	r.POST("/restore", restoreSelected)
	r.POST("/delete-selected", deleteSelected)
	r.GET("/{id}/preview", previewFile)
	r.POST("/{id}/retention", setRetention)
	r.DELETE("/{id}", deleteFile)
	r.POST("/upload", uploadFile)
	r.POST("/sync", syncNow)
	// Sync sources (Settings tab)
	r.GET("/sources", listSources)
	r.POST("/sources", saveSource)
	r.DELETE("/sources/{sid}", deleteSource)
	r.GET("/sources/check", checkSource)
	r.GET("/sources/ls", lsDir)
	r.GET("/sources/home", homeDirHandler)
	r.GET("/sources/detect", detectPaths)
	r.GET("/sources/presets", listPresets)
	r.GET("/settings", getSettings)
	r.POST("/settings", saveSettings)
	// Adjacency-list explorer
	r.GET("/tree", treeChildren)
	r.GET("/roots", listRoots)
}

func storagePage(c *tool.Ctx) {
	c.HTML(StoragePage(c.Base()))
}

func listFiles(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	rows, err := globalSyncMgr.ListAll(c.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	providerFilter := c.Query("provider")
	instanceFilter := c.Query("instance")

	type fileRow struct {
		ID           uint   `json:"id"`
		ProviderType string `json:"provider_type"`
		InstanceName string `json:"instance_name"`
		RelPath      string `json:"rel_path"`
		Size         int    `json:"size"`
		SyncedAt     string `json:"synced_at"`
		RetentionDays int   `json:"retention_days"`
	}

	out := make([]fileRow, 0, len(rows))
	for _, r := range rows {
		if providerFilter != "" && r.ProviderType != providerFilter {
			continue
		}
		if instanceFilter != "" && r.InstanceName != instanceFilter {
			continue
		}
		out = append(out, fileRow{
			ID:            r.ID,
			ProviderType:  r.ProviderType,
			InstanceName:  r.InstanceName,
			RelPath:       r.RelPath,
			Size:          len(r.Content),
			SyncedAt:      r.SyncedAt.Format("2006-01-02 15:04:05"),
			RetentionDays: r.RetentionDays,
		})
	}
	c.JSON(http.StatusOK, out)
}

func restoreSelected(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	if err := c.R.ParseForm(); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rawIDs := c.R.Form["ids"]
	ids := make([]uint, 0, len(rawIDs))
	for _, s := range rawIDs {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, uint(n))
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "no ids provided"})
		return
	}

	sources, err := globalSyncMgr.ListSources(c.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	srcsByInstance := make(map[string][]providersync.SrcInfo)
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		key := src.ProviderType + "/" + src.InstanceName
		srcsByInstance[key] = append(srcsByInstance[key], providersync.SrcInfo{Mode: src.Mode, SyncPath: src.SyncPath})
	}

	count, err := globalSyncMgr.RestoreSelected(c.Context(), ids, srcsByInstance)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]int{"restored": count})
}

func deleteSelected(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	if err := c.R.ParseForm(); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	deleted := 0
	for _, s := range c.R.Form["instance"] {
		parts := strings.SplitN(s, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if n, err := globalSyncMgr.DeleteByInstance(c.Context(), parts[0], parts[1]); err == nil {
			deleted += int(n)
		}
	}
	for _, s := range c.R.Form["ids"] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			continue
		}
		if err := globalSyncMgr.DeleteByID(c.Context(), uint(n)); err == nil {
			deleted++
		}
	}
	c.JSON(http.StatusOK, map[string]int{"deleted": deleted})
}

func previewFile(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	idStr := c.PathValue("id")
	n, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	row, err := globalSyncMgr.GetByID(c.Context(), uint(n))
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	const maxPreview = 1 << 20 // 1 MB
	if len(row.Content) > maxPreview {
		c.JSON(http.StatusOK, map[string]any{"too_large": true, "size": len(row.Content)})
		return
	}
	if !utf8.Valid(row.Content) {
		c.JSON(http.StatusOK, map[string]any{"binary": true, "size": len(row.Content)})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":       row.ID,
		"rel_path": row.RelPath,
		"content":  string(row.Content),
		"size":     len(row.Content),
	})
}

func setRetention(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	idStr := c.PathValue("id")
	n, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	days, err := strconv.Atoi(c.Form("days"))
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid days"})
		return
	}
	if err := globalSyncMgr.SetRetention(c.Context(), uint(n), days); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func deleteFile(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	idStr := c.PathValue("id")
	n, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := globalSyncMgr.DeleteByID(c.Context(), uint(n)); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func uploadFile(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	if err := c.R.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	providerType := c.Form("provider_type")
	instanceName := c.Form("instance_name")
	relPath := c.Form("rel_path")
	if providerType == "" || instanceName == "" || relPath == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider_type, instance_name, rel_path required"})
		return
	}
	f, _, err := c.R.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "file required: " + err.Error()})
		return
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := globalSyncMgr.Upload(c.Context(), providerType, instanceName, relPath, content); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func syncNow(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	sources, err := globalSyncMgr.ListSources(c.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	synced := 0
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		ins := provider.Instance{
			Type:    provider.Type(src.ProviderType),
			Name:    src.InstanceName,
			Storage: &provider.StorageConfig{Mode: src.Mode, SyncPath: src.SyncPath},
		}
		if err := globalSyncMgr.SyncOne(c.Context(), ins); err != nil {
			log.Warn().Err(err).Str("provider", src.ProviderType).Str("path", src.SyncPath).Msg("providersync: syncNow failed")
		} else {
			synced++
		}
	}
	c.JSON(http.StatusOK, map[string]int{"synced": synced})
}

// ── Sync Sources ──────────────────────────────────────────────────────

func listSources(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	rows, err := globalSyncMgr.ListSources(c.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rows)
}

func saveSource(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	var src entity.ProviderStorageSource
	if err := json.NewDecoder(c.R.Body).Decode(&src); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if src.ProviderType == "" || src.SyncPath == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider_type, sync_path required"})
		return
	}
	if src.InstanceName == "" {
		src.InstanceName = src.ProviderType
	}
	if src.Mode == "" {
		src.Mode = "folder"
	}
	saved, err := globalSyncMgr.SaveSource(c.Context(), src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, saved)
}

func deleteSource(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	idStr := c.PathValue("sid")
	n, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := globalSyncMgr.DeleteSource(c.Context(), uint(n)); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func checkSource(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	mode := c.Query("mode")
	if mode == "" {
		mode = "folder"
	}
	syncPath := c.Query("path")
	if syncPath == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	files, err := globalSyncMgr.CheckSource(mode, syncPath)
	if err != nil {
		c.JSON(http.StatusOK, map[string]any{"error": err.Error(), "files": []string{}})
		return
	}
	sort.Strings(files)
	c.JSON(http.StatusOK, map[string]any{"files": files, "count": len(files)})
}

func homeDirHandler(c *tool.Ctx) {
	home, _ := os.UserHomeDir()
	c.JSON(http.StatusOK, map[string]string{"path": home})
}

// lsDir lists one directory level — name, type (dir/file), full path.
func lsDir(c *tool.Ctx) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		c.JSON(http.StatusOK, map[string]any{"error": err.Error(), "entries": []any{}})
		return
	}
	type entry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Path  string `json:"path"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, entry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Path:  path + "/" + e.Name(),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"entries": out})
}

// detectedPath is one discovered config path for a provider.
type detectedPath struct {
	Label    string `json:"label"`
	SyncPath string `json:"sync_path"`
	Mode     string `json:"mode"`
	Exists   bool   `json:"exists"`
}

// knownProviderPaths returns the well-known config paths for each provider
// resolved against the real home directory.
func knownProviderPaths(providerType, home string) []detectedPath {
	switch providerType {
	case "claude":
		return []detectedPath{
			{Label: "Claude config folder", SyncPath: filepath.Join(home, ".claude"), Mode: "folder"},
			{Label: "Claude credentials", SyncPath: filepath.Join(home, ".claude", ".credentials.json"), Mode: "single"},
		}
	case "codex":
		return []detectedPath{
			{Label: "Codex config folder", SyncPath: filepath.Join(home, ".codex"), Mode: "folder"},
		}
	case "gemini":
		return []detectedPath{
			{Label: "Gemini config folder", SyncPath: filepath.Join(home, ".gemini"), Mode: "folder"},
		}
	case "wick":
		agentsBase := agentconfig.ResolveBaseDir(agentconfig.WorkspaceConfig{})
		return []detectedPath{
			{Label: "Wick config folder", SyncPath: filepath.Dir(agentsBase), Mode: "folder"},
			{Label: "Wick agents folder", SyncPath: agentsBase, Mode: "folder"},
		}
	}
	return nil
}

func listPresets(c *tool.Ctx) {
	home, _ := os.UserHomeDir()
	pt := c.Query("provider_type")
	providers := []string{"claude", "codex", "gemini"}
	if pt != "" {
		providers = []string{pt}
	}
	type presetRow struct {
		ProviderType string `json:"provider_type"`
		Label        string `json:"label"`
		SyncPath     string `json:"sync_path"`
		Mode         string `json:"mode"`
	}
	var out []presetRow
	for _, p := range providers {
		for _, d := range knownProviderPaths(p, home) {
			out = append(out, presetRow{ProviderType: p, Label: d.Label, SyncPath: d.SyncPath, Mode: d.Mode})
		}
	}
	c.JSON(http.StatusOK, out)
}

// detectPaths returns well-known paths for a provider, each tagged with
// whether they exist on disk. Used by the UI to show a fast checklist.
func detectPaths(c *tool.Ctx) {
	pt := c.Query("provider")
	if pt == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider required"})
		return
	}
	home, _ := os.UserHomeDir()
	paths := knownProviderPaths(pt, home)
	if paths == nil {
		paths = []detectedPath{}
	}
	for i := range paths {
		_, err := os.Stat(paths[i].SyncPath)
		paths[i].Exists = err == nil
	}
	c.JSON(http.StatusOK, paths)
}

// ── Adjacency-list Explorer ───────────────────────────────────────────

func treeChildren(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	providerType := c.Query("provider")
	instanceName := c.Query("instance")
	if providerType == "" || instanceName == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider and instance required"})
		return
	}
	parentID := uint(0)
	if raw := c.Query("parent_id"); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid parent_id"})
			return
		}
		parentID = uint(n)
	}
	rows, err := globalSyncMgr.ListChildren(c.Context(), providerType, instanceName, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type childRow struct {
		ID            uint   `json:"id"`
		Name          string `json:"name"`
		IsDir         bool   `json:"is_dir"`
		Size          int    `json:"size"`
		SyncedAt      string `json:"synced_at"`
		RetentionDays int    `json:"retention_days"`
	}
	out := make([]childRow, 0, len(rows))
	for _, r := range rows {
		row := childRow{
			ID:            r.ID,
			Name:          r.Name,
			IsDir:         r.IsDir,
			RetentionDays: r.RetentionDays,
		}
		if !r.IsDir {
			row.Size = len(r.Content)
			row.SyncedAt = r.SyncedAt.Format("2006-01-02 15:04:05")
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, out)
}

func listRoots(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not initialised"})
		return
	}
	roots, err := globalSyncMgr.ListRoots(c.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, roots)
}

// ── Global Settings ───────────────────────────────────────────────────

const defaultSyncIntervalSeconds = 5

func getSettings(c *tool.Ctx) {
	syncInterval := defaultSyncIntervalSeconds
	if globalConfigs != nil {
		if v := globalConfigs.GetOwned("provider-storage", "sync_interval_seconds"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				syncInterval = n
			}
		}
	}
	c.JSON(http.StatusOK, map[string]any{"sync_interval_seconds": syncInterval})
}

func saveSettings(c *tool.Ctx) {
	if globalConfigs == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "configs not initialised"})
		return
	}
	var body struct {
		SyncIntervalSeconds int `json:"sync_interval_seconds"`
	}
	if err := json.NewDecoder(c.R.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.SyncIntervalSeconds <= 0 {
		body.SyncIntervalSeconds = defaultSyncIntervalSeconds
	}
	if err := globalConfigs.SetOwned(context.Background(), "provider-storage", "sync_interval_seconds", strconv.Itoa(body.SyncIntervalSeconds)); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
