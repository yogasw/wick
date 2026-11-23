package hostapi

import (
	"context"
	"slices"
	"strings"
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

var _ AdminDirectory = (*ManifestDirectory)(nil)

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

// refreshIfStale re-reads installed manifests periodically, including for
// keys already known. A CLI-installed plugin can replace its manifest while
// Wick stays running; keeping the old HostScopes/HostResources indefinitely
// would incorrectly deny newly declared operations until a server restart.
func (d *ManifestDirectory) refreshIfStale() {
	d.mu.Lock()
	stale := time.Since(d.at) > rescanAfter
	d.mu.Unlock()
	if stale {
		d.Reload(context.Background())
	}
}

// Plugin resolves key and checks for a changed manifest after rescanAfter.
// A successful lookup must not bypass refresh: upgrades keep the same key.
func (d *ManifestDirectory) Plugin(key string) (PluginInfo, bool) {
	d.refreshIfStale()
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.plugins[key]
	return p, ok
}

// All lists every installed plugin, sorted by key (the admin Host access
// page). Refresh here too so the admin can enable new scopes immediately.
func (d *ManifestDirectory) All() []PluginInfo {
	d.refreshIfStale()
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]PluginInfo, 0, len(d.plugins))
	for _, p := range d.plugins {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b PluginInfo) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// UnknownScopes lists the host_scopes key declares that this wick does not
// know (denied; shown to the admin).
func (d *ManifestDirectory) UnknownScopes(key string) []string {
	d.refreshIfStale()
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.unknown[key])
}
