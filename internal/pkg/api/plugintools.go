package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/ui"
	pluginreplace "github.com/yogasw/wick/internal/plugins/replace"
	"github.com/yogasw/wick/internal/tools"
	toolplugin "github.com/yogasw/wick/internal/tools/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// pluginToolRoutes serves /tools/{key} for tool plugins. Built-in tools are
// mounted once on the boot mux; a tool plugin can be installed, updated,
// enabled or disabled while wick runs, so its routes live here instead,
// keyed by tool key and swapped under a lock. Each plugin still goes
// through a toolRouter (same Ctx, renderer, webhook groups) and the same
// RequireToolAccess check as a built-in, so only the mounting differs.
type pluginToolRoutes struct {
	cfg tool.ConfigReader
	// gate wraps a plugin's mux with the per-tool access check.
	gate func(meta login.ToolMeta, next http.Handler) http.Handler

	mu    sync.RWMutex
	byKey map[string]*pluginToolRoute
}

type pluginToolRoute struct {
	open     http.Handler // webhook groups: no login, the plugin verifies
	gated    http.Handler // everything else: RequireToolAccess first
	hooks    []string
	webhooks []tool.WebhookRoute
}

func newPluginToolRoutes(cfg tool.ConfigReader, gate func(login.ToolMeta, http.Handler) http.Handler) *pluginToolRoutes {
	return &pluginToolRoutes{cfg: cfg, gate: gate, byKey: map[string]*pluginToolRoute{}}
}

// build turns module m into its route entry without mounting it: the same
// toolRouter pass boot runs, so a module boot would refuse is refused here.
func (p *pluginToolRoutes) build(m tool.Module) (*pluginToolRoute, error) {
	meta := m.Meta
	meta.Path = "/tools/" + meta.Key
	tr := newToolRouter(p.cfg)
	tr.withScope(meta, len(m.Configs) > 0, m.Register)
	if err := tr.validate(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	tr.mount(mux)
	return &pluginToolRoute{
		open:     mux,
		gated:    p.gate(login.ToolMeta{Path: meta.Path, DefaultVisibility: meta.DefaultVisibility}, mux),
		hooks:    tr.WebhookPrefixes(),
		webhooks: tr.WebhookRoutes(),
	}, nil
}

// set mounts (or replaces) the routes of plugin module m.
func (p *pluginToolRoutes) set(m tool.Module) error {
	e, err := p.build(m)
	if err != nil {
		return err
	}
	p.put(m.Meta.Key, e)
	// Same audit line boot writes for the built-ins' open surface.
	if len(e.hooks) > 0 {
		log.Info().Str("tool", m.Meta.Key).Strs("prefixes", e.hooks).
			Msg("tool plugin: webhook groups mounted without authentication — handlers own request verification")
	}
	return nil
}

func (p *pluginToolRoutes) put(key string, e *pluginToolRoute) {
	p.mu.Lock()
	p.byKey[key] = e
	p.mu.Unlock()
}

func (p *pluginToolRoutes) get(key string) *pluginToolRoute {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.byKey[key]
}

// block answers 404 for every path of key — a built-in that a live-loaded
// plugin replaced still has its routes on the boot mux, which this shadows.
func (p *pluginToolRoutes) block(key string) {
	nf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ui.RenderNotFound(w, r, login.GetUser(r.Context()), http.StatusNotFound)
	})
	p.put(key, &pluginToolRoute{open: nf, gated: nf})
}

func (p *pluginToolRoutes) remove(key string) {
	p.mu.Lock()
	delete(p.byKey, key)
	p.mu.Unlock()
}

func (p *pluginToolRoutes) lookup(path string) *pluginToolRoute {
	rest, ok := strings.CutPrefix(path, "/tools/")
	if !ok {
		return nil
	}
	key, _, _ := strings.Cut(rest, "/")
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.byKey[key]
}

// wrap serves a loaded plugin's paths and hands every other /tools/ path to
// fallback (the built-in tools).
func (p *pluginToolRoutes) wrap(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := p.lookup(r.URL.Path)
		if e == nil {
			fallback.ServeHTTP(w, r)
			return
		}
		if isWebhookPath(r.URL.Path, e.hooks) {
			e.open.ServeHTTP(w, r)
			return
		}
		e.gated.ServeHTTP(w, r)
	})
}

// WebhookRoutes lists the webhook routes of every loaded plugin, sorted like
// toolRouter.WebhookRoutes.
func (p *pluginToolRoutes) WebhookRoutes() []tool.WebhookRoute {
	p.mu.RLock()
	var out []tool.WebhookRoute
	for _, e := range p.byKey {
		out = append(out, e.webhooks...)
	}
	p.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// validatePluginModule is the check every tool plugin passes before it is
// registered, at boot and live alike: a valid meta, a key no built-in uses,
// and routes the tool router accepts. Boot turns a failure into an Error
// status for that plugin instead of a failed start.
func validatePluginModule(m tool.Module) error {
	if err := tool.ValidateModules([]tool.Module{m}); err != nil {
		return err
	}
	// The pool unloads its own previous version first, so a key still in the
	// registry belongs to a built-in tool.
	if tools.Has(m.Meta.Key) {
		return toolplugin.ErrKeyTaken(m.Meta.Key)
	}
	tr := newToolRouter(nil)
	meta := m.Meta
	meta.Path = "/tools/" + meta.Key
	tr.withScope(meta, len(m.Configs) > 0, m.Register)
	return tr.validate()
}

// liveToolSink is the toolplugin.Sink of a running wick: what boot does for a
// tool (validate, replace, seed config rows and default tags, mount routes,
// register), done for one plugin at a time. Calls are serialised by the pool.
type liveToolSink struct {
	routes *pluginToolRoutes
	// ensureConfigs seeds the plugin's config rows (configs.Service.EnsureOwned).
	ensureConfigs func(ctx context.Context, owner string, rows ...entity.Config) error
	// ensureTags seeds the default access tags (tags.Service.EnsureToolDefaultTags).
	ensureTags func(ctx context.Context, path string, defaults []tool.DefaultTag) error
	// migrate copies config / access from replaced keys (pluginreplace ApplyAll).
	migrate func(ctx context.Context, pairs []pluginreplace.Pair)

	// replaced holds the modules a plugin took over (old key → module), so
	// unloading the plugin brings them back. onBootMux marks the ones whose
	// routes are on the boot mux (replaced live: unblocking is enough);
	// the others were replaced at boot and never mounted, so their entry is
	// built once on first restore and kept, never registering them twice.
	replaced  map[string]tool.Module
	onBootMux map[string]bool
	restored  map[string]*pluginToolRoute
}

func newLiveToolSink(routes *pluginToolRoutes) *liveToolSink {
	return &liveToolSink{routes: routes, replaced: map[string]tool.Module{}, onBootMux: map[string]bool{},
		restored: map[string]*pluginToolRoute{}}
}

// rememberBootReplaced records the modules boot's Prepare unregistered for a
// plugin (before is tools.All() taken before Prepare ran).
func (s *liveToolSink) rememberBootReplaced(before []tool.Module, pairs []pluginreplace.Pair) {
	for _, p := range pairs {
		if p.Kind != pluginreplace.KindTool {
			continue
		}
		for _, m := range before {
			if m.Meta.Key == p.Old {
				s.replaced[p.Old] = m
			}
		}
	}
}

func (s *liveToolSink) Add(m tool.Module) error {
	if err := validatePluginModule(m); err != nil {
		return err
	}
	ctx := context.Background()
	if s.ensureConfigs != nil && len(m.Configs) > 0 {
		if err := s.ensureConfigs(ctx, m.Meta.Key, m.Configs...); err != nil {
			return err
		}
	}
	if s.ensureTags != nil && len(m.Meta.DefaultTags) > 0 {
		if err := s.ensureTags(ctx, "/tools/"+m.Meta.Key, m.Meta.DefaultTags); err != nil {
			return err
		}
	}
	if err := s.routes.set(m); err != nil {
		return err
	}
	tools.Register(m)
	s.replace(ctx, m)
	return nil
}

// replace does live what boot's pluginreplace.Prepare + ApplyAll do: the
// tools m replaces leave the registry, their paths answer 404, and their
// config / access move over to m.
func (s *liveToolSink) replace(ctx context.Context, m tool.Module) {
	if len(m.Meta.Replaces) == 0 {
		return
	}
	before := tools.All()
	pairs := pluginreplace.Prepare(nil, []tool.Module{m}, nil, func(old string) bool {
		for _, b := range before {
			if b.Meta.Key == old {
				s.replaced[old] = b
				s.onBootMux[old] = s.routes.get(old) == nil
			}
		}
		s.routes.block(old)
		return tools.Unregister(old)
	})
	if s.migrate != nil && len(pairs) > 0 {
		s.migrate(ctx, pairs)
	}
}

func (s *liveToolSink) Remove(key string) {
	s.routes.remove(key)
	tools.Unregister(key)
	// Bring back what this plugin replaced, as if it had never loaded.
	for _, p := range pluginreplace.Forget(key) {
		if p.Kind != pluginreplace.KindTool {
			continue
		}
		old, ok := s.replaced[p.Old]
		if !ok {
			s.routes.remove(p.Old)
			continue
		}
		delete(s.replaced, p.Old)
		switch {
		case s.onBootMux[p.Old]:
			s.routes.remove(p.Old) // unblock: the boot mux serves it again
		case s.restored[p.Old] != nil:
			s.routes.put(p.Old, s.restored[p.Old])
		default:
			e, err := s.routes.build(old)
			if err != nil {
				log.Warn().Str("tool", p.Old).Err(err).Msg("tool plugin: replaced tool could not be restored")
				s.routes.remove(p.Old)
				continue
			}
			s.restored[p.Old] = e
			s.routes.put(p.Old, e)
		}
		if !tools.Has(p.Old) {
			tools.Register(old)
		}
		log.Info().Str("tool", p.Old).Str("by", key).Msg("tool plugin: replaced tool restored")
	}
}

// liveToolItems is tools.All() as tool.Tool entries with the mount path
// stamped, read per call so a plugin loaded or unloaded at runtime shows up
// (or disappears) in every list built from it.
func liveToolItems() []tool.Tool {
	mods := tools.All()
	out := make([]tool.Tool, 0, len(mods))
	for _, m := range mods {
		meta := m.Meta
		meta.Path = "/tools/" + meta.Key
		out = append(out, meta)
	}
	return out
}

// pluginReloaders is the reconciler the Plugins admin triggers after an
// install, enable or disable: connector plugins (when any are loaded) and
// tool plugins, so both take effect without a reload.
type pluginReloaders struct {
	conn  *connplugin.Reloader
	tools *toolplugin.Pool
}

func (r pluginReloaders) Reload(ctx context.Context) {
	if r.conn != nil {
		r.conn.Reload(ctx)
	}
	if r.tools != nil {
		r.tools.Reconcile()
	}
}
