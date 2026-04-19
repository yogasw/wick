package postgres

import (
	"github.com/yogasw/wick/internal/entity"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) {
	// Legacy rename: app_variables → configs. Runs once; on fresh DBs
	// neither table exists and this is a no-op. On existing DBs the
	// new name doesn't exist yet so the rename proceeds; on a
	// re-migrated DB the new name is present and we skip.
	if err := renameConfigsTable(db); err != nil {
		log.Fatal().Msgf("rename app_variables → configs: %s", err.Error())
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
	)
	if err != nil {
		log.Fatal().Msgf("failed to run migration: %s", err.Error())
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
