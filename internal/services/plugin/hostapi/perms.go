package hostapi

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// BlockAll is the scope and item of the "Block all host access" overlay row.
const BlockAll = "*"

// Effective status of a (plugin, scope), as the admin page colours it.
const (
	StatusAllowed      = "allowed"
	StatusBlocked      = "blocked"
	StatusUndeclared   = "undeclared"     // used but not in the manifest; allowed in phase 1
	StatusOffByDefault = "off-by-default" // default-deny scope nobody allowed yet
	StatusUnknown      = "unknown"        // declared but not known to this wick; denied
)

// PermissionStore persists the admin overlay. Small on purpose: the admin
// UI needs exactly list, set and clear.
type PermissionStore interface {
	List(ctx context.Context, pluginKey string) ([]entity.PluginHostPermission, error)
	Set(ctx context.Context, p entity.PluginHostPermission) error
	Delete(ctx context.Context, pluginKey, scope, item string) error
}

// Decision is the effective answer for one call.
type Decision struct {
	Allowed bool
	Status  string
	// Reason says why a call was denied, for the PermissionDenied message.
	Reason string
}

type permKey struct{ scope, item string }

// Permissions decides scope access from the manifest, the overlay and the
// scope defaults. Overlay rows are cached per plugin; Set/Clear/Invalidate
// drop the cache so an admin change applies to the next call.
type Permissions struct {
	store PermissionStore

	mu    sync.Mutex
	cache map[string]map[permKey]bool
}

// NewPermissions wraps store (nil = no overlay, defaults only).
func NewPermissions(store PermissionStore) *Permissions {
	return &Permissions{store: store, cache: map[string]map[permKey]bool{}}
}

// overlay returns pluginKey's overlay rows. An error means the store could
// not be read; callers fail closed (an unreadable "Block all" must not turn
// into allow).
func (p *Permissions) overlay(ctx context.Context, pluginKey string) (map[permKey]bool, error) {
	if p == nil || p.store == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.cache[pluginKey]; ok {
		return m, nil
	}
	rows, err := p.store.List(ctx, pluginKey)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("plugin", pluginKey).Msg("host api: permission store unreadable, denying")
		return nil, err
	}
	m := make(map[permKey]bool, len(rows))
	for _, r := range rows {
		m[permKey{r.Scope, r.Item}] = r.Allowed
	}
	p.cache[pluginKey] = m
	return m, nil
}

// Invalidate drops the cached overlay of pluginKey.
func (p *Permissions) Invalidate(pluginKey string) {
	p.mu.Lock()
	delete(p.cache, pluginKey)
	p.mu.Unlock()
}

// Set stores one overlay decision (item "" = the whole scope; BlockAll for
// both = block every scope of the plugin).
func (p *Permissions) Set(ctx context.Context, pluginKey, scope, item string, allowed bool, by string) error {
	err := p.store.Set(ctx, entity.PluginHostPermission{PluginKey: pluginKey, Scope: scope, Item: item,
		Allowed: allowed, UpdatedBy: by, UpdatedAt: time.Now().UTC()})
	p.Invalidate(pluginKey)
	return err
}

// Clear removes one overlay decision, returning it to the default.
func (p *Permissions) Clear(ctx context.Context, pluginKey, scope, item string) error {
	err := p.store.Delete(ctx, pluginKey, scope, item)
	p.Invalidate(pluginKey)
	return err
}

// Decide answers whether plugin may call under scope. item is the table, the
// "METHOD /path" or the "connector.op" the call reaches; "" asks about the
// scope as a whole (whoami's scopes_allowed). Order: whoami is always
// allowed; an unreadable store denies; Block all; unknown scope;
// declaration (rest/db/connectors/config need the scope and the item
// declared); scope overlay or default; item overlay.
func (p *Permissions) Decide(ctx context.Context, plugin PluginInfo, scope, item string) Decision {
	if scope == wickplugin.ScopeHostWhoami {
		return Decision{Allowed: true, Status: StatusAllowed}
	}
	ov, err := p.overlay(ctx, plugin.Key)
	if err != nil {
		return Decision{Status: StatusBlocked, Reason: "permission store unavailable"}
	}
	if allowed, ok := ov[permKey{BlockAll, BlockAll}]; ok && !allowed {
		return Decision{Status: StatusBlocked, Reason: "all host access of this plugin is blocked by an admin"}
	}
	sc, ok := wickplugin.LookupHostScope(scope)
	if !ok {
		return Decision{Status: StatusUnknown, Reason: "unknown scope"}
	}
	declared := slices.Contains(plugin.Scopes, scope)
	if sc.Resource != "" {
		if !declared {
			return Decision{Status: StatusBlocked, Reason: "scope not declared in host_scopes"}
		}
		if item != "" && !slices.Contains(plugin.Resources.Items(sc.Resource), item) {
			return Decision{Status: StatusBlocked, Reason: "not declared in host_resources." + sc.Resource}
		}
	}
	allowed, ok := ov[permKey{scope, ""}]
	if !ok {
		allowed = sc.DefaultAllow
		if !allowed {
			return Decision{Status: StatusOffByDefault, Reason: "scope is off until an admin allows it"}
		}
	}
	if !allowed {
		return Decision{Status: StatusBlocked, Reason: "scope blocked by an admin"}
	}
	if item != "" {
		if a, ok := ov[permKey{scope, item}]; ok && !a {
			return Decision{Status: StatusBlocked, Reason: "item blocked by an admin"}
		}
	}
	if !declared {
		return Decision{Allowed: true, Status: StatusUndeclared}
	}
	return Decision{Allowed: true, Status: StatusAllowed}
}

// ScopeStatus is one row of the admin "what this plugin can do" card.
type ScopeStatus struct {
	Scope    string
	Declared bool
	Decision Decision
}

// Statuses lists every official scope plus the unknown ones the plugin
// declared, each with its scope-level decision (W4 admin card).
func (p *Permissions) Statuses(ctx context.Context, plugin PluginInfo) []ScopeStatus {
	var out []ScopeStatus
	for _, sc := range wickplugin.HostScopes {
		out = append(out, ScopeStatus{Scope: sc.Name, Declared: slices.Contains(plugin.Scopes, sc.Name),
			Decision: p.Decide(ctx, plugin, sc.Name, "")})
	}
	for _, s := range plugin.Scopes {
		if _, ok := wickplugin.LookupHostScope(s); !ok {
			out = append(out, ScopeStatus{Scope: s, Declared: true, Decision: Decision{Status: StatusUnknown, Reason: "unknown scope"}})
		}
	}
	return out
}

// WarnUnknownScopes logs the host_scopes of a loaded manifest this wick does
// not know. The plugin still loads (§8); calls under them are denied.
func WarnUnknownScopes(ctx context.Context, m wickplugin.Manifest) {
	if unknown := m.UnknownHostScopes(); len(unknown) > 0 {
		log.Ctx(ctx).Warn().Str("plugin", m.Module.Meta.Key).Strs("scopes", unknown).
			Msg("host api: plugin declares scopes this wick does not know; they are denied")
	}
}

// MemoryPermissionStore is an in-memory PermissionStore (tests, or a wick
// running without a database).
type MemoryPermissionStore struct {
	mu   sync.Mutex
	rows map[string]entity.PluginHostPermission
}

// NewMemoryPermissionStore returns an empty store.
func NewMemoryPermissionStore() *MemoryPermissionStore {
	return &MemoryPermissionStore{rows: map[string]entity.PluginHostPermission{}}
}

func memKey(plugin, scope, item string) string { return plugin + "\x00" + scope + "\x00" + item }

// List implements PermissionStore.
func (m *MemoryPermissionStore) List(_ context.Context, pluginKey string) ([]entity.PluginHostPermission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []entity.PluginHostPermission
	for _, r := range m.rows {
		if r.PluginKey == pluginKey {
			out = append(out, r)
		}
	}
	return out, nil
}

// Set implements PermissionStore.
func (m *MemoryPermissionStore) Set(_ context.Context, p entity.PluginHostPermission) error {
	m.mu.Lock()
	m.rows[memKey(p.PluginKey, p.Scope, p.Item)] = p
	m.mu.Unlock()
	return nil
}

// Delete implements PermissionStore.
func (m *MemoryPermissionStore) Delete(_ context.Context, pluginKey, scope, item string) error {
	m.mu.Lock()
	delete(m.rows, memKey(pluginKey, scope, item))
	m.mu.Unlock()
	return nil
}
