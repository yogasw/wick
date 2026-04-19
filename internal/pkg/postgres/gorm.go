package postgres

import (
	"github.com/yogasw/wick/internal/pkg/config"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/rs/zerolog/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func NewGORM(c config.Database) *gorm.DB {
	var dialector gorm.Dialector

	if strings.HasPrefix(c.URL, "postgres://") || strings.HasPrefix(c.URL, "postgresql://") {
		dialector = postgres.Open(c.URL)
	} else {
		dialector = sqlite.Open(c.URL)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: NewLogLevel("warn"),
	})
	if err != nil {
		log.Fatal().Msgf("failed to opening db conn: %s", err.Error())
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal().Msgf("failed to get db object: %s", err.Error())
	}

	if strings.HasPrefix(c.URL, "postgres") {
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(100)
		sqlDB.SetConnMaxLifetime(time.Hour)
	} else {
		// SQLite: single writer
		sqlDB.SetMaxOpenConns(1)
	}

	return db
}
