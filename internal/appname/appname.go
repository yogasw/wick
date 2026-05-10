// Package appname is the single source of truth for the running
// app's *path-safe identifier* — the slug used in `~/.<app>/` for
// DB, logs, agents Layout, gate spec/socket. Every artefact lands
// under one tree.
//
// Resolution order (first non-empty wins):
//
//  1. BuildAppName  — ldflag-baked at compile time by `wick build`
//  2. wick.yml      — top-level `name:` field, walked from cwd
//  3. "wick"        — last-ditch fallback
//
// `APP_NAME` env var is intentionally NOT in this chain. That env
// holds the human-readable display label (e.g. "My App" with spaces
// and capitalization) used by the UI / login / systray title — it
// must not contaminate filesystem paths. Path identifiers come from
// `name:` in wick.yml, which already enforces no-space slug rules.
//
// The package has zero deps on the rest of the codebase so any
// other internal package can import it without risking cycles.
package appname

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// BuildAppName is the ldflag injection target. Builder writes here
// via `-X github.com/yogasw/wick/internal/appname.BuildAppName=<name>`.
// Empty when not built via `wick build` (e.g. `go run`, VSCode debug).
var BuildAppName = ""

var (
	resolveOnce sync.Once
	resolved    string
)

// Resolve returns the active app name. Result is cached for the
// process lifetime — chain inputs (BuildAppName, env, yml) don't
// change at runtime, so we resolve exactly once.
func Resolve() string {
	resolveOnce.Do(func() {
		resolved = resolve()
	})
	return resolved
}

func resolve() string {
	if BuildAppName != "" {
		return BuildAppName
	}
	for _, path := range []string{"wick.yml", "../wick.yml", "../../wick.yml"} {
		if data, err := os.ReadFile(path); err == nil {
			var cfg struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(data, &cfg) == nil && cfg.Name != "" {
				return cfg.Name
			}
		}
	}
	return "wick"
}
