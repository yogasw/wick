package plugin

import (
	"slices"
	"sync"
)

// declared is what DeclareHostAccess recorded for this binary.
var declared struct {
	mu        sync.Mutex
	scopes    []string
	resources *HostResources
}

// DeclareHostAccess records the Host API scopes (and, for rest.*, db.*,
// connectors.execute and config.read_secret, the host_resources) this plugin
// binary uses. Call it in main before Serve / ServeJob / ServeTool /
// service.ServeService: the self-built manifest (--dump-manifest, so `wick
// plugin build`) then carries them as host_scopes and host_resources, and
// an invalid declaration fails the dump instead of the install. A later
// call replaces an earlier one. See docs/plugins/host-api.md.
func DeclareHostAccess(scopes []string, resources *HostResources) {
	declared.mu.Lock()
	defer declared.mu.Unlock()
	declared.scopes = slices.Clone(scopes)
	declared.resources = resources
}

// applyHostAccess copies the DeclareHostAccess values onto m.
func applyHostAccess(m *Manifest) error {
	declared.mu.Lock()
	m.HostScopes = slices.Clone(declared.scopes)
	m.HostResources = declared.resources
	declared.mu.Unlock()
	return m.ValidateHostAccess()
}
