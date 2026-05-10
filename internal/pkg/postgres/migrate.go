package postgres

import (
	"encoding/json"

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
	)
	if err != nil {
		log.Fatal().Msgf("failed to run migration: %s", err.Error())
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
