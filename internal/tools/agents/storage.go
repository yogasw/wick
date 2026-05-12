package agents

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/providersync"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

const previewMaxBytes = 1024 * 1024 // 1 MB

// StorageFileVM is the view model for one file row in the storage table.
type StorageFileVM struct {
	entity.ProviderStorage
	SyncedAtFmt string
}

// StoragePageVM is the view model for the storage manager page.
type StoragePageVM struct {
	Base           string
	Files          []StorageFileVM
	FilterProvider string
	FilterInstance string
	ProviderTypes  []string
}

// storagePage renders the Provider Storage Manager page.
func storagePage(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.Error(http.StatusServiceUnavailable, "sync manager not ready")
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	files, err := globalSyncMgr.ListAll(ctx)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}

	filterProvider := c.Query("provider")
	filterInstance := c.Query("instance")

	var filtered []StorageFileVM
	for _, f := range files {
		if filterProvider != "" && f.ProviderType != filterProvider {
			continue
		}
		if filterInstance != "" && f.InstanceName != filterInstance {
			continue
		}
		filtered = append(filtered, StorageFileVM{
			ProviderStorage: f,
			SyncedAtFmt:     f.SyncedAt.Format("2006-01-02 15:04:05"),
		})
	}

	types := make([]string, 0)
	seen := map[string]bool{}
	for _, t := range provider.SupportedTypes() {
		k := string(t)
		if !seen[k] {
			types = append(types, k)
			seen[k] = true
		}
	}

	c.JSON(http.StatusOK, StoragePageVM{
		Base:           c.Base(),
		Files:          filtered,
		FilterProvider: filterProvider,
		FilterInstance: filterInstance,
		ProviderTypes:  types,
	})
}

// storageRestoreSelected restores selected file IDs to filesystem.
// POST /providers/storage/restore  body: ids=1&ids=2&ids=3
func storageRestoreSelected(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not ready"})
		return
	}

	rawIDs := c.R.Form["ids"]
	if len(rawIDs) == 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "no ids provided"})
		return
	}
	ids := make([]uint, 0, len(rawIDs))
	for _, s := range rawIDs {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id: " + s})
			return
		}
		ids = append(ids, uint(n))
	}

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	count, err := globalSyncMgr.RestoreSelected(ctx, ids, buildSrcsByInstance())
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("storage restore: %s", err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"restored": count})
}

// storagePreview returns the content of one file for preview.
// GET /providers/storage/{id}/preview
func storagePreview(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not ready"})
		return
	}
	id, err := parseIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	row, err := globalSyncMgr.GetByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	if len(row.Content) > previewMaxBytes {
		c.JSON(http.StatusOK, map[string]any{
			"too_large": true,
			"size":      len(row.Content),
			"rel_path":  row.RelPath,
		})
		return
	}

	isBinary := !utf8.Valid(row.Content)
	c.JSON(http.StatusOK, map[string]any{
		"rel_path":  row.RelPath,
		"size":      len(row.Content),
		"is_binary": isBinary,
		"content":   string(row.Content),
	})
}

// storageSetRetention updates retention_days for one file row.
// POST /providers/storage/{id}/retention  body: days=7
func storageSetRetention(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not ready"})
		return
	}
	id, err := parseIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	days, err := strconv.Atoi(strings.TrimSpace(c.Form("days")))
	if err != nil || days < 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid days"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	if err := globalSyncMgr.SetRetention(ctx, id, days); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": id, "retention_days": days})
}

// storageDelete removes one file row from DB.
// DELETE /providers/storage/{id}
func storageDelete(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not ready"})
		return
	}
	id, err := parseIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	if err := globalSyncMgr.DeleteByID(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// storageUpload handles manual file upload into DB.
// POST /providers/storage/upload  multipart: provider_type, instance_name, rel_path, file
func storageUpload(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not ready"})
		return
	}
	if err := c.R.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "parse form: " + err.Error()})
		return
	}
	providerType := strings.TrimSpace(c.Form("provider_type"))
	instanceName := strings.TrimSpace(c.Form("instance_name"))
	relPath := strings.TrimSpace(c.Form("rel_path"))
	if providerType == "" || instanceName == "" || relPath == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider_type, instance_name, rel_path required"})
		return
	}

	var content []byte
	fh, _, err := c.R.FormFile("file")
	if err == nil {
		defer func(fh multipart.File) { _ = fh.Close() }(fh)
		buf := make([]byte, 10<<20)
		n, _ := fh.Read(buf)
		content = buf[:n]
	} else {
		// fallback: plain text body field
		content = []byte(c.Form("content"))
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	if err := globalSyncMgr.Upload(ctx, providerType, instanceName, relPath, content); err != nil {
		log.Ctx(c.Context()).Error().Msgf("storage upload: %s", err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "uploaded", "rel_path": relPath})
}

// ── helpers ───────────────────────────────────────────────────────────

func parseIDParam(c *tool.Ctx) (uint, error) {
	raw := c.PathValue("id")
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid id %q", raw)
	}
	return uint(n), nil
}

func buildSrcsByInstance() map[string][]providersync.SrcInfo {
	instances, _ := provider.Load()
	m := make(map[string][]providersync.SrcInfo, len(instances))
	for _, ins := range instances {
		if ins.Storage != nil {
			key := string(ins.Type) + "/" + ins.Name
			m[key] = append(m[key], providersync.SrcInfo{Mode: ins.Storage.Mode, SyncPath: ins.Storage.SyncPath})
		}
	}
	return m
}

// instanceSyncBase resolves the filesystem base for restore.
func instanceSyncBase(ins provider.Instance) string {
	if ins.Storage == nil {
		return ""
	}
	return filepath.Dir(ins.Storage.SyncPath)
}
