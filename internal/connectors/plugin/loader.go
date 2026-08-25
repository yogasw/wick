package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"

	connectors "github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Found is one discovered plugin: its key, on-disk binary, and parsed manifest
// envelope.
type Found struct {
	Key        string
	BinaryPath string
	Manifest   wickplugin.Manifest
}

// Scan walks dir/<name>/plugin.json and returns one Found per connector. Each
// plugin.json is a manifest envelope; the binary is resolved from the
// manifest's Entry (falling back to the directory name). A missing dir is not
// an error (returns nil) — plugins are optional.
func Scan(dir string) ([]Found, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan plugins: %w", err)
	}
	var out []Found
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		manifestPath := filepath.Join(dir, name, "plugin.json")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var env wickplugin.Manifest
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if env.Module.Meta.Key == "" {
			continue
		}
		entry := env.Entry
		if entry == "" {
			entry = name
		}
		out = append(out, Found{
			Key:        env.Module.Meta.Key,
			BinaryPath: filepath.Join(dir, name, entry),
			Manifest:   env,
		})
	}
	return out, nil
}

// registerFn matches connectors.Register; injected for tests.
type registerFn func(connector.Module)

// loadWith is the testable core of Load. mgr may be nil in tests that only
// assert registration (the closures are not invoked). When enabled is non-nil,
// keys for which it returns false are skipped (not verified, not registered);
// a nil enabled treats all discovered plugins as enabled.
func loadWith(dir string, register registerFn, mgr *Manager, enabled func(string) bool) (int, error) {
	found, err := Scan(dir)
	if err != nil {
		return 0, err
	}
	var getConn ConnGetter
	if mgr != nil {
		getConn = func(key string) (*Lease, error) { return mgr.Client(key) }
	} else {
		getConn = func(string) (*Lease, error) {
			return nil, fmt.Errorf("no manager configured")
		}
	}
	count := 0
	for _, f := range found {
		if err := wickplugin.ValidateKey(f.Key); err != nil {
			log.Warn().Str("plugin", f.Key).Err(err).Msg("connector plugin loader: skipped (invalid key)")
			continue
		}
		if enabled != nil && !enabled(f.Key) {
			continue
		}
		// Route by kind: this loader owns connectors only. A tool/job manifest
		// that lands in the connectors dir is skipped (not mis-registered as a
		// connector) — tool/job kinds get their own loader + dir when their
		// host adapters land (§18).
		if k := wickplugin.NormalizeKind(f.Manifest.Kind); k != wickplugin.KindConnector {
			log.Warn().Str("plugin", f.Key).Str("kind", k).Msg("connector plugin loader: skipped non-connector kind")
			continue
		}
		if err := wickplugin.VerifyManifest(f.Manifest, f.BinaryPath); err != nil {
			log.Warn().Str("connector", f.Key).Err(err).Msg("connector plugin: skipped (verification failed)")
			continue
		}
		register(BuildModule(f.Manifest.Module, getConn))
		count++
	}
	return count, nil
}

// Load scans dir, builds a Manager over the discovered binaries, registers
// each plugin module via connectors.Register (replace-by-key so a plugin
// overrides the compiled-in builtin of the same key), and returns the
// Manager (caller owns its KillAll on shutdown). Returns a nil Manager when
// no plugins are present. When enabled is non-nil, keys for which it returns
// false are excluded from the Manager (not spawnable) and not registered; a
// nil enabled treats all discovered plugins as enabled.
func Load(dir string, idleTimeout time.Duration, enabled func(string) bool) (*Manager, int, error) {
	found, err := Scan(dir)
	if err != nil {
		return nil, 0, err
	}
	binaries := make(map[string]string, len(found))
	for _, f := range found {
		if enabled != nil && !enabled(f.Key) {
			continue
		}
		binaries[f.Key] = f.BinaryPath
	}
	// Always return a Manager, even with zero plugins on disk. Returning nil
	// here used to leave pluginMgr nil, which skipped building the hot-reload
	// poller — so a plugin installed AFTER a pluginless boot was never picked
	// up (no poller to register it, ever). An empty Manager costs nothing and
	// keeps the reload path alive for the first install.
	mgr := NewManager(binaries, idleTimeout)
	n, err := loadWith(dir, connectors.Register, mgr, enabled)
	if err != nil {
		mgr.KillAll()
		return nil, 0, err
	}
	return mgr, n, nil
}
