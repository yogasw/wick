package postgres

import (
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/entity"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) {
	err := db.AutoMigrate(
		&entity.User{},
		&entity.Session{},
		&entity.ToolPermission{},
		&entity.Tag{},
		&entity.ToolTag{},
		&entity.UserTag{},
		&entity.Bookmark{},
		&entity.Config{},
		&entity.SSOProvider{},
		&entity.Job{},
		&entity.JobRun{},
		&entity.Connector{},
		&entity.ConnectorOperation{},
		&entity.ConnectorRun{},
		&entity.ConnectorAccount{},
		&entity.CustomConnector{},
		&entity.CustomConnectorMCPServer{},
		&entity.PersonalAccessToken{},
		&entity.PushSubscription{},
		&entity.OAuthClient{},
		&entity.OAuthAuthorizationCode{},
		&entity.OAuthToken{},
		&entity.AgentChannel{},
		&entity.ProviderStorage{},
		&entity.ProviderStorageSource{},
		&entity.DataTable{},
		&entity.DataTableRow{},
		// Workflow storage migration — see
		// internal/planning/archive/workflow/svelte-migration.md. Tables added in
		// parallel with the existing file-based store; the importer in
		// internal/agents/workflow/repository (future phase) hydrates
		// the rows from disk on boot before any handler reads them.
		&entity.Workflow{},
		&entity.WorkflowVersion{},
		&entity.WorkflowTestCase{},
		&entity.Skill{},
	)
	if err != nil {
		log.Fatal().Msgf("failed to run migration: %s", err.Error())
	}

	// Create adjacency-list unique index — not managed by AutoMigrate.
	// Soft-fail: a DB with duplicate (provider, instance, parent_id, name)
	// rows would reject the unique constraint; the runtime still works via
	// SELECT-then-INSERT, just without the DB-level guard. Re-runs on every
	// boot via IF NOT EXISTS, so it installs once duplicates are cleared.
	if res := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_tree ON provider_storage (provider_type, instance_name, parent_id, name)`); res.Error != nil {
		log.Warn().Err(res.Error).Msg("migrate: idx_storage_tree creation failed (duplicates present?)")
	}
}

// repairProviderStorageTree rewires every row's parent_id from its rel_path
// so a row at "/a/b/c" is parented to the row at "/a/b". Heals DBs where
// an ancestor row was deleted but descendants still reference the dead ID.
// Idempotent: returns 0 fixed when the tree is already healthy.
func RepairProviderStorageTree(db *gorm.DB) (int, error) {
	var rows []entity.ProviderStorage
	// Only the adjacency columns are needed to rewire parent_id. Skip the
	// Content bytea blob — pulling it would clone every file's bytes into
	// heap (gigabytes of transient alloc) for a tree walk that never reads it.
	if err := db.Select("id", "provider_type", "instance_name", "rel_path", "parent_id").
		Find(&rows).Error; err != nil {
		return 0, err
	}
	const sep = "\x00"
	byKey := make(map[string]uint, len(rows))
	for _, r := range rows {
		byKey[r.ProviderType+sep+r.InstanceName+sep+r.RelPath] = r.ID
	}
	fixed := 0
	for _, r := range rows {
		norm := filepath.ToSlash(r.RelPath)
		leadingSlash := strings.HasPrefix(norm, "/")
		trimmed := strings.TrimPrefix(norm, "/")
		parts := strings.Split(trimmed, "/")
		var wantParent uint
		if len(parts) > 1 {
			parentRel := strings.Join(parts[:len(parts)-1], "/")
			if leadingSlash {
				parentRel = "/" + parentRel
			}
			wantParent = byKey[r.ProviderType+sep+r.InstanceName+sep+parentRel]
			// parent rel_path not found → fall through with wantParent=0
			// so the row stays reachable via listRoots.
		}
		if r.ParentID == wantParent {
			continue
		}
		if err := db.Model(&entity.ProviderStorage{}).
			Where("id = ?", r.ID).
			Update("parent_id", wantParent).Error; err != nil {
			return fixed, err
		}
		fixed++
	}
	return fixed, nil
}
