package project

import (
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// MigrateWorkspacesToProjects is idempotent: it is a no-op when any
// project already exists on disk. Converts each workspace into a
// project with the same name, folder, and defaults, then relinks all
// sessions from workspace name to project_id.
//
// Safety:
//   - Skips entirely if projects/ is non-empty.
//   - os.Rename is used for managed files/ — atomic on same-FS.
//   - Session meta is written only after the project is created.
//   - Legacy workspaces/ dir is kept on disk (not deleted) for safety.
func MigrateWorkspacesToProjects(layout config.Layout, newID func() string, relink func(wsName, projectID string) error) error {
	// Idempotent: if any project exists, migration already ran.
	ids, err := List(layout)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		return nil
	}

	wsNames, err := workspace.List(layout)
	if err != nil || len(wsNames) == 0 {
		return err
	}

	log.Info().Int("count", len(wsNames)).Msg("project.migrate: migrating workspaces to projects")

	wsToProject := make(map[string]string, len(wsNames))
	for _, name := range wsNames {
		ws, loadErr := workspace.Load(layout, name)
		if loadErr != nil {
			log.Warn().Err(loadErr).Str("workspace", name).Msg("project.migrate: skip broken workspace")
			continue
		}

		id := newID()
		_, createErr := Create(layout, CreateOptions{
			ID:          id,
			Name:        name,
			Icon:        "📁",
			Description: ws.Meta.Description,
			CustomPath:  ws.Meta.CustomPath,
			Defaults: Defaults{
				Preset:   ws.Meta.DefaultPreset,
				Provider: ws.Meta.DefaultProvider,
			},
			Tags: ws.Meta.Tags,
		})
		if createErr != nil {
			log.Error().Err(createErr).Str("workspace", name).Msg("project.migrate: create project failed")
			continue
		}

		// Move managed files/ atomically.
		if ws.Meta.CustomPath == "" {
			src := layout.WorkspaceManagedPath(name)
			dst := layout.ProjectManagedPath(id)
			if storage.PathExists(src) {
				// Remove dst created by Create (empty dir), then rename src.
				_ = os.Remove(dst)
				if mvErr := os.Rename(src, dst); mvErr != nil {
					// Non-fatal: fallback — dst is an empty dir, src stays.
					log.Warn().Err(mvErr).Str("workspace", name).Msg("project.migrate: rename managed files failed; leaving in place")
					// Recreate the empty managed dir so project is still valid.
					_ = os.MkdirAll(dst, 0o755)
				}
			}
		}

		wsToProject[name] = id
		log.Info().Str("workspace", name).Str("project_id", id).Msg("project.migrate: migrated")
	}

	// Relink sessions.
	for wsName, pid := range wsToProject {
		if err := relink(wsName, pid); err != nil {
			log.Warn().Err(err).Str("workspace", wsName).Msg("project.migrate: relink sessions failed")
		}
	}

	// Legacy workspaces/ dir intentionally left for safety. User can delete manually.
	log.Info().Msg("project.migrate: done (legacy workspaces/ kept on disk)")
	return nil
}

// RelinkSessions is the concrete relink callback used at boot: scans
// all session dirs and updates meta.project_id for sessions that
// reference the old workspace name.
// Uses a raw map round-trip to preserve all existing fields.
func RelinkSessions(layout config.Layout, wsName, projectID string) error {
	sessionDirs, err := storage.ScanDirNames(layout.SessionsDir())
	if err != nil {
		return err
	}
	for _, sid := range sessionDirs {
		metaPath := filepath.Join(layout.SessionsDir(), sid, "meta.json")
		var raw map[string]any
		if readErr := storage.ReadJSON(metaPath, &raw); readErr != nil {
			continue
		}
		ws, _ := raw["workspace"].(string)
		if ws != wsName {
			continue
		}
		raw["project_id"] = projectID
		delete(raw, "workspace")
		if writeErr := storage.WriteJSON(metaPath, &raw); writeErr != nil {
			log.Warn().Err(writeErr).Str("session", sid).Msg("project.migrate: relink session failed")
		}
	}
	return nil
}
