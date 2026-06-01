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
	"os"
	"strings"

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
	KeyAllowedOrigins       = "allowed_origins"
	KeySessionSecret        = "session_secret"
	KeyAdminPasswordChanged = "admin_password_changed"
	KeyEncryptionKey        = "encryption_key"
	KeyStartupScript        = "startup_script"
	KeyStartupScriptEnabled = "startup_script_enabled"
)

// generators maps app-level keys to the function that produces a fresh
// value on first-boot (when the row is missing) and on admin-triggered
// Regenerate(). Keep this tight — only real secrets belong here.
var generators = map[string]func() string{
	KeySessionSecret: generateHex32,
	KeyEncryptionKey: generateHex32,
}

// envOverrides maps app-level keys to env-var names that override the
// DB value at read time. Useful for bootstrap on remote hosts where
// the seeded localhost app_url blocks the admin UI behind its own
// host allowlist — exporting APP_URL lets the operator break in
// without first reaching the UI to flip the row. The admin UI marks
// these rows as read-only while the env var is set so it is obvious
// where the value is coming from.
var envOverrides = map[string]string{
	KeyAppURL:         "APP_URL",
	KeyAllowedOrigins: "ALLOWED_ORIGINS",
	KeyEncryptionKey:  "WICK_ENC_KEY",
}

// EnvOverrideFor reports the env-var override for a key, if any. Returns
// (envName, value, true) only when the env var is declared in
// envOverrides AND currently set to a non-empty value.
func EnvOverrideFor(key string) (envName, value string, set bool) {
	envName = envOverrides[key]
	if envName == "" {
		return "", "", false
	}
	v := strings.TrimSpace(os.Getenv(envName))
	if v == "" {
		return "", "", false
	}
	return envName, v, true
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
			Value:       "http://localhost:9425",
			Description: "Base URL where this app is reachable. Used to build the OAuth callback URL and other absolute links. Update after moving the app behind a new domain.",
		},
		{
			Key:         KeyAllowedOrigins,
			Type:        "kvlist",
			Options:     "url",
			Value:       "[]",
			Description: "Extra URLs that may reach the admin/host allowlist beyond app_url. Add one row per origin (e.g. http://192.168.1.42:9425) when you need to open the app from another device on the same network. The ALLOWED_ORIGINS env var (comma-separated) overrides this list — handy for Termux/LAN bootstrap before the admin UI is reachable.",
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
			Hidden:      true,
		},
		{
			Key:         KeyStartupScriptEnabled,
			Type:        "bool",
			Value:       "false",
			Description: "Run the startup script below in a shell when the server boots. Output is captured to logs/startup-script-YYYY-MM-DD.log. The process is killed when the server stops.",
		},
		{
			Key:         KeyStartupScript,
			Type:        "textarea",
			Value:       "",
			Description: "Shell script run on server boot (sh on Linux/macOS, PowerShell on Windows). Use it to launch a tunnel like `ngrok http 9425` or `cloudflared tunnel run my-tunnel` so the local port stays unexposed. Edit + restart the server (tray menu) to apply.",
			VisibleWhen: "startup_script_enabled:true",
		},
		{
			Key:           KeyEncryptionKey,
			Type:          "text",
			Value:         generateHex32(),
			Description:   "Master key for the encrypted-fields layer (HKDF salt = user_uuid, AES-256-GCM). Regenerating invalidates every wick_enc_ token already issued. Set WICK_ENC_KEY in the environment to override the DB value (vault injection); set WICK_ENC_DISABLE=true to disable encryption entirely.",
			IsSecret:      true,
			CanRegenerate: true,
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
