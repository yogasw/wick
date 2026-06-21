package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	connectors "github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Found is one discovered plugin: its key, on-disk binary, and raw manifest.
type Found struct {
	Key        string
	BinaryPath string
	Manifest   []byte
}

// Scan walks dir/<name>/plugin.json and returns one Found per connector. The
// binary is the sibling file named after the directory (e.g. demo/demo). A
// missing dir is not an error (returns nil) — plugins are optional.
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
		var mod connector.Module
		if err := json.Unmarshal(raw, &mod); err != nil {
			continue
		}
		out = append(out, Found{
			Key:        mod.Meta.Key,
			BinaryPath: filepath.Join(dir, name, name),
			Manifest:   raw,
		})
	}
	return out, nil
}

// registerFn matches connectors.Register; injected for tests.
type registerFn func(connector.Module)

// loadWith is the testable core of Load. mgr may be nil in tests that only
// assert registration (the closures are not invoked).
func loadWith(dir string, register registerFn, mgr *Manager) (int, error) {
	found, err := Scan(dir)
	if err != nil {
		return 0, err
	}
	var getConn ConnGetter
	if mgr != nil {
		getConn = func(key string) (wickplugin.GRPCConn, error) { return mgr.Client(key) }
	} else {
		getConn = func(string) (wickplugin.GRPCConn, error) {
			return nil, fmt.Errorf("no manager configured")
		}
	}
	count := 0
	for _, f := range found {
		mod, err := BuildModule(f.Manifest, getConn)
		if err != nil {
			return count, fmt.Errorf("build %q: %w", f.Key, err)
		}
		register(mod)
		count++
	}
	return count, nil
}

// Load scans dir, builds a Manager over the discovered binaries, registers
// each plugin module via connectors.Register (replace-by-key so a plugin
// overrides the compiled-in builtin of the same key), and returns the
// Manager (caller owns its KillAll on shutdown). Returns a nil Manager when
// no plugins are present.
func Load(dir string, idleTimeout time.Duration) (*Manager, int, error) {
	found, err := Scan(dir)
	if err != nil {
		return nil, 0, err
	}
	if len(found) == 0 {
		return nil, 0, nil
	}
	binaries := make(map[string]string, len(found))
	for _, f := range found {
		binaries[f.Key] = f.BinaryPath
	}
	mgr := NewManager(binaries, idleTimeout)
	n, err := loadWith(dir, connectors.Register, mgr)
	if err != nil {
		mgr.KillAll()
		return nil, 0, err
	}
	return mgr, n, nil
}
