// Package configs manages runtime-editable configuration stored in
// the `configs` table. Adding a new app-level knob is a two-step
// change:
//
//  1. Declare a default in appDefaults() below + add its key constant.
//  2. (Optional) Add a typed accessor on Service — e.g. AppURL(),
//     SessionSecret() — so callers don't juggle strings.
//
// Module-level configs live in each module's Config struct; the
// framework reflects those at boot via entity.StructToConfigs.
//
// Values live in a cache guarded by RWMutex, populated at startup and
// refreshed on every Set() — so hot-reload is transparent to callers.
package configs

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/yogasw/wick/internal/entity"
)

// Canonical key constants. Always reference a variable by these rather
// than the string literal — renaming then becomes a one-line change
// the compiler catches everywhere.
const (
	KeyAppName              = "app_name"
	DefaultAppName          = "Wick Mini Tools"
	KeyAppDescription       = "app_description"
	DefaultAppDescription   = "A lightweight internal tooling platform — build, deploy, and run custom tools for your team in minutes."
	KeyAppURL               = "app_url"
	KeySessionSecret        = "session_secret"
	KeyAdminPasswordChanged = "admin_password_changed"
)

// generators maps app-level keys to the function that produces a fresh
// value on first-boot (when the row is missing) and on admin-triggered
// Regenerate(). Keep this tight — only real secrets belong here.
var generators = map[string]func() string{
	KeySessionSecret: generateHex32,
}

// appDefaults returns the seed rows reconciled into `configs` on every
// boot. Existing rows win — these defaults only fill in what is
// missing. Metadata columns (description, is_secret, required, ...)
// are always refreshed so renaming/retagging never needs a migration.
func appDefaults() []entity.Config {
	return []entity.Config{
		{
			Key:         KeyAppName,
			Type:        "text",
			Value:       DefaultAppName,
			Description: "Display name shown in the browser tab, navbar, login page, and home hero. Change it to rebrand the entire UI in one place.",
		},
		{
			Key:         KeyAppDescription,
			Type:        "text",
			Value:       DefaultAppDescription,
			Description: "Short tagline shown below the app name on the home hero. Describe what this instance is for.",
		},
		{
			Key:         KeyAppURL,
			Type:        "url",
			Value:       "http://localhost:8080",
			Description: "Base URL where this app is reachable. Used to build the OAuth callback URL and other absolute links. Update after moving the app behind a new domain.",
		},
		{
			Key:           KeySessionSecret,
			Type:          "text",
			Value:         generateHex32(),
			Description:   "HMAC secret used to sign session cookies. Regenerating invalidates every active session — all users get logged out.",
			IsSecret:      true,
			CanRegenerate: true,
		},
		{
			Key:         KeyAdminPasswordChanged,
			Type:        "bool",
			Value:       "false",
			Description: "Set to true once the default admin password has been changed. Used to show a security warning on startup.",
		},
	}
}

// generateHex32 returns 64 hex chars (32 random bytes) — the default
// generator for secret keys.
func generateHex32() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
