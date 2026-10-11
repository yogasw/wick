package hostapi

import (
	"context"
	"slices"
	"sync"
	"time"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// rescanAfter bounds how often a lookup miss rescans the plugin folders (a
// plugin installed after start).
const rescanAfter = 10 * time.Second

// Manifests lists the installed manifests of one kind (connplugin.ScanKind
// over the kind's folder).
type Manifests func(kind string) ([]wickplugin.Manifest, error)

// ManifestDirectory is the PluginDirectory over the installed plugin
// manifests. Loading a manifest warns about host_scopes this wick does not
// know (WarnUnknownScopes) and keeps them for the admin page.
type ManifestDirectory struct {
	scan Manifests
	// ConfigOwner names the configs owner of a plugin's own rows per kind
	// ("" = the kind has no plugin config store).
	ConfigOwner func(kind, key string) string

	mu      sync.Mutex
	plugins map[string]PluginInfo
	unknown map[string][]string
	at      time.Time
}

var _ PluginDirectory = (*ManifestDirectory)(nil)

// NewManifestDirectory returns a directory over scan; call Reload once the
// plugins are installed.
func NewManifestDirectory(scan Manifests) *ManifestDirectory {
	return &ManifestDirectory{scan: scan, plugins: map[string]PluginInfo{}, unknown: map[string][]string{}}
}

// Reload rescans every kind.
func (d *ManifestDirectory) Reload(ctx context.Context) {
	plugins, unknown := map[string]PluginInfo{}, map[string][]string{}
	for _, kind := range wickplugin.Kinds {
		ms, err := d.scan(kind)
		if err != nil {
			continue
		}
		for _, m := range ms {
			key := m.Module.Meta.Key
			if key == "" {
				continue
			}
			WarnUnknownScopes(ctx, m)
			if u := m.UnknownHostScopes(); len(u) > 0 {
				unknown[key] = u
			}
			info := PluginInfo{Key: key, Kind: kind, Version: m.Version, Scopes: slices.Clone(m.HostScopes), Resources: m.HostResources}
			if d.ConfigOwner != nil {
				info.ConfigOwner = d.ConfigOwner(kind, key)
			}
			plugins[key] = info
		}
	}
	d.mu.Lock()
	d.plugins, d.unknown, d.at = plugins, unknown, time.Now()
	d.mu.Unlock()
}

// Plugin resolves key; a miss rescans at most every rescanAfter.
func (d *ManifestDirectory) Plugin(key string) (PluginInfo, bool) {
	d.mu.Lock()
	p, ok := d.plugins[key]
	stale := time.Since(d.at) > rescanAfter
	d.mu.Unlock()
	if ok || !stale {
		return p, ok
	}
	d.Reload(context.Background())
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok = d.plugins[key]
	return p, ok
}

// UnknownScopes lists the host_scopes key declares that this wick does not
// know (denied; shown to the admin).
func (d *ManifestDirectory) UnknownScopes(key string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.unknown[key])
}
