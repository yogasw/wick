package config

import (
	"github.com/caarlos0/env/v9"
	"github.com/rs/zerolog/log"
)

// Load parses environment variables into Config. Every field has a
// sensible default so running the binary without a .env still boots.
// App-level runtime knobs (app_url, session_secret) and SSO credentials
// live in the database, not here — see internal/configs and internal/sso.
func Load() *Config {
	var c Config
	if err := env.Parse(&c); err != nil {
		log.Fatal().Msgf("unable to parse env: %s", err.Error())
	}
	return &c
}

type Config struct {
	App      App
	Database Database
}

// App holds env-only values. Anything user-editable at runtime belongs
// in the `app_variables` table instead.
type App struct {
	// Comma-separated emails that are automatically granted admin role
	// on first login. Env-only so an admin can't remove themselves from
	// the approver list via the UI.
	AdminEmails string `env:"APP_ADMIN_EMAILS" envDefault:"admin@admin.com"`
	// Default password used once to seed an admin account when the DB
	// has no admin user yet. Ignored on every subsequent boot.
	AdminPassword string `env:"APP_ADMIN_PASSWORD" envDefault:"admin"`
	// Fallback APP_NAME used only when the DB has no `app_name` row yet
	// (first boot). Once written to the DB, the DB value wins.
	Name string `env:"APP_NAME" envDefault:""`
	// Fallback APP_URL used only when the DB has no `app_url` row yet
	// (first boot). Once written to the DB, the DB value wins.
	URL string `env:"APP_URL" envDefault:"http://localhost:8080"`
	// HTTP listen port. CLI --port flag overrides this.
	Port int `env:"PORT" envDefault:"8080"`
}

type Database struct {
	URL string `env:"DATABASE_URL" envDefault:"wick.db"`
}
