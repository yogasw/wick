package postgres

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Migrate(db *gorm.DB) {
	// Legacy rename: app_variables → configs. Runs once; on fresh DBs
	// neither table exists and this is a no-op. On existing DBs the
	// new name doesn't exist yet so the rename proceeds; on a
	// re-migrated DB the new name is present and we skip.
	if err := renameConfigsTable(db); err != nil {
		log.Fatal().Msgf("rename app_variables → configs: %s", err.Error())
	}

	// Move connector credential blobs from connectors.configs (JSON
	// text) into the configs table (one row per field, owner =
	// "connector:{id}"). Runs before AutoMigrate so the source column
	// is still present, then drops the column to lock the migration in.
	if err := migrateConnectorConfigsToConfigs(db); err != nil {
		log.Fatal().Msgf("migrate connector configs: %s", err.Error())
	}

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
		&entity.PersonalAccessToken{},
		&entity.OAuthClient{},
		&entity.OAuthAuthorizationCode{},
		&entity.OAuthToken{},
		&entity.AgentChannel{},
		&entity.ProviderStorage{},
		&entity.ProviderStorageSource{},
	)
	if err != nil {
		log.Fatal().Msgf("failed to run migration: %s", err.Error())
	}

	// Backfill name for rows predating adjacency-list migration.
	db.Exec(`UPDATE provider_storage SET name = REPLACE(rel_path, RTRIM(rel_path, REPLACE(rel_path, '/', '')), '') WHERE name = '' AND rel_path != ''`)

	// One-shot data migration to the absolute-path scheme: drop rows with
	// pre-fix relative rel_path so the sync ticker re-captures from disk
	// using absolute keys. Idempotent: matches no rows after first boot.
	if res := db.Where("rel_path NOT LIKE '/%' AND rel_path NOT LIKE '_:%'").
		Delete(&entity.ProviderStorage{}); res.Error != nil {
		log.Warn().Err(res.Error).Msg("migrate: wipe legacy provider_storage rows failed")
	} else if res.RowsAffected > 0 {
		log.Info().Int64("rows", res.RowsAffected).Msg("migrate: wiped legacy provider_storage rows (pre-absolute-path)")
	}

	// One-shot data migration: split the legacy exclude_patterns column
	// on provider_storage_sources into separate Mode="exclude" rows, then
	// drop the column. Lets exclude live as a first-class source kind
	// instead of an opaque text property. Idempotent — column absent on
	// fresh DBs and on the second boot.
	if err := migrateExcludePatternsToRows(db); err != nil {
		log.Warn().Err(err).Msg("migrate: split exclude_patterns failed")
	}

	// Re-parent orphan rows: rewires parent_id from rel_path so that
	// listChildren works even when an ancestor row was previously deleted
	// (drive-letter row rotation, etc.). Idempotent.
	if n, err := repairProviderStorageTree(db); err != nil {
		log.Warn().Err(err).Msg("migrate: repair provider_storage tree failed")
	} else if n > 0 {
		log.Info().Int("rows", n).Msg("migrate: repaired orphan provider_storage parent_id")
	}

	// Create adjacency-list unique index — not managed by AutoMigrate.
	// Soft-fail: a prod DB with duplicate (provider, instance, parent_id,
	// name) rows from old buggy code would reject the unique constraint;
	// the runtime still works via SELECT-then-INSERT, just without the
	// DB-level guard. Re-run after `Repair Tree` to install the index.
	if res := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_tree ON provider_storage (provider_type, instance_name, parent_id, name)`); res.Error != nil {
		log.Warn().Err(res.Error).Msg("migrate: idx_storage_tree creation failed (duplicates present?)")
	}

	// Remove stale per-field Slack/Telegram rows from configs table.
	// Runs after AutoMigrate so configs table exists. Idempotent.
	if err := removeChannelConfigsFromConfigs(db); err != nil {
		log.Warn().Msgf("remove channel configs from configs table: %s", err.Error())
	}
}

func renameConfigsTable(db *gorm.DB) error {
	m := db.Migrator()
	if m.HasTable("configs") {
		return nil
	}
	if !m.HasTable("app_variables") {
		return nil
	}
	return m.RenameTable("app_variables", "configs")
}

// migrateConnectorConfigsToConfigs is a one-shot migration that lifts
// the legacy connectors.configs JSON blob into per-field rows on the
// configs table (owner = "connector:{id}"), then drops the source
// column. Idempotent — once the column is gone every subsequent boot
// short-circuits.
func migrateConnectorConfigsToConfigs(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("connectors") {
		return nil
	}
	if !m.HasColumn("connectors", "configs") {
		return nil
	}
	type row struct {
		ID      string
		Configs string
	}
	var rows []row
	if err := db.Raw(`SELECT id, configs FROM connectors WHERE configs IS NOT NULL AND configs <> '' AND configs <> '{}'`).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		var legacy map[string]string
		if err := json.Unmarshal([]byte(r.Configs), &legacy); err != nil {
			continue
		}
		owner := "connector:" + r.ID
		for k, v := range legacy {
			if k == "" || v == "" {
				continue
			}
			cfg := entity.Config{
				Owner: owner,
				Key:   k,
				Value: v,
			}
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&cfg).Error; err != nil {
				return err
			}
		}
	}
	// Both postgres and sqlite (≥3.35) support ALTER TABLE DROP COLUMN.
	// Bypass gorm's migrator — its sqlite driver panics in recreateTable
	// when the field has been removed from the entity struct.
	return db.Exec(`ALTER TABLE connectors DROP COLUMN configs`).Error
}

// removeChannelConfigsFromConfigs deletes the per-field Slack and Telegram
// config rows that used to live in the configs table under owner="agents".
// Since these moved to agent_channels, the old rows are stale noise.
// Idempotent — deletes rows by key name, safe to run on fresh DBs too.
//
// WARNING: these key names ("mode", "bot_token", etc.) are generic and could
// theoretically collide with future configs under owner="agents". The delete
// is scoped to owner="agents" only, so non-agent config rows are safe. If a
// new config key shares a name listed here it will be incorrectly deleted on
// first boot — rename it or remove it from this list instead.
// migrateExcludePatternsToRows splits the legacy exclude_patterns text
// column on provider_storage_sources into separate Mode="exclude" rows
// (one per non-empty, non-comment line), then drops the column. Idempotent:
// short-circuits when the column no longer exists.
func migrateExcludePatternsToRows(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("provider_storage_sources") || !m.HasColumn("provider_storage_sources", "exclude_patterns") {
		return nil
	}
	type legacyRow struct {
		ID              uint
		ProviderType    string
		InstanceName    string
		SyncPath        string
		Mode            string
		ExcludePatterns string
		Enabled         bool
	}
	var rows []legacyRow
	if err := db.Raw(`SELECT id, provider_type, instance_name, sync_path, mode, exclude_patterns, enabled
		FROM provider_storage_sources
		WHERE exclude_patterns IS NOT NULL AND exclude_patterns <> ''`).Scan(&rows).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, r := range rows {
		for _, ln := range strings.Split(r.ExcludePatterns, "\n") {
			pat := strings.TrimSpace(ln)
			if pat == "" || strings.HasPrefix(pat, "#") {
				continue
			}
			// Skip if an identical exclude row already exists (rerun safety).
			var n int64
			db.Model(&entity.ProviderStorageSource{}).
				Where("provider_type = ? AND instance_name = ? AND mode = ? AND sync_path = ?",
					r.ProviderType, r.InstanceName, "exclude", pat).
				Count(&n)
			if n > 0 {
				continue
			}
			lbl := pat
			if len(lbl) > 120 {
				lbl = lbl[len(lbl)-120:]
			}
			row := entity.ProviderStorageSource{
				ProviderType: r.ProviderType,
				InstanceName: r.InstanceName,
				Label:        lbl,
				SyncPath:     pat,
				Mode:         "exclude",
				Enabled:      r.Enabled,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := db.Create(&row).Error; err != nil {
				return err
			}
		}
	}
	return db.Exec(`ALTER TABLE provider_storage_sources DROP COLUMN exclude_patterns`).Error
}

// repairProviderStorageTree rewires every row's parent_id from its rel_path
// so a row at "/a/b/c" is parented to the row at "/a/b". Heals DBs where
// an ancestor row was deleted but descendants still reference the dead ID.
// Idempotent: returns 0 fixed when the tree is already healthy.
func repairProviderStorageTree(db *gorm.DB) (int, error) {
	var rows []entity.ProviderStorage
	if err := db.Find(&rows).Error; err != nil {
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

func removeChannelConfigsFromConfigs(db *gorm.DB) error {
	channelKeys := []string{
		// Slack
		"mode", "bot_token", "app_token", "signing_secret",
		"access_mode", "allowed_users", "allowed_groups",
		"slack_workspace",
		// Telegram (old prefixed keys)
		"telegram_bot_token", "telegram_allowed_ids", "telegram_workspace",
	}
	return db.Where("owner = ? AND key IN ?", "agents", channelKeys).
		Delete(&entity.Config{}).Error
}
