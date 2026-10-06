// Package plugin is the host side of tool plugins: a kind=tool binary under
// plugins/tools/<key>/ registers as an ordinary tool (grid, palette, access
// rules, config page) whose routes reverse-proxy to the plugin's HTTP server
// on a unix socket. The process is spawned on first use, held for at most
// DefaultHold, and idle-killed by the pool sweeper unless keep_warm.
package plugin

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/rs/zerolog/log"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/pkg/upgrade"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// DefaultIdle is how long an unused tool plugin stays running;
// WICK_TOOL_PLUGIN_IDLE (a Go duration) overrides it.
const DefaultIdle = 10 * time.Minute

func idleTTL() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("WICK_TOOL_PLUGIN_IDLE"))); err == nil && d > 0 {
		return d
	}
	return DefaultIdle
}

// Pool holds every loaded tool plugin's Runner and sweeps idle ones, using
// the same rule as the connector plugin sweeper: not warm, nothing in
// flight, unused for longer than the idle TTL.
type Pool struct {
	mu       sync.Mutex
	runners  map[string]*Runner
	versions map[string]string
	ttl      time.Duration
	stop     chan struct{}
	once     sync.Once
}

// NewPool returns an empty pool; call Start once tools are loaded.
func NewPool() *Pool {
	return &Pool{runners: map[string]*Runner{}, versions: map[string]string{}, ttl: idleTTL(), stop: make(chan struct{})}
}

func (p *Pool) add(key, version string, r *Runner) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runners[key] = r
	p.versions[key] = version
}

// Remove stops the process of tool plugin key and drops it from the pool,
// ahead of an uninstall deleting its files.
func (p *Pool) Remove(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r, ok := p.runners[key]; ok {
		r.Stop()
	}
	delete(p.runners, key)
	delete(p.versions, key)
}

// Version returns the installed version of tool plugin key; ok is false for
// a built-in tool.
func (p *Pool) Version(key string) (string, bool) {
	if p == nil {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.versions[key]
	return v, ok
}

// Inflight sums in-flight proxied requests (graceful reload waits on it).
func (p *Pool) Inflight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, r := range p.runners {
		n += int(r.Inflight())
	}
	return n
}

// Sweep runs one idle pass and returns how many processes were killed.
func (p *Pool) Sweep() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, r := range p.runners {
		if r.sweep(p.ttl) {
			n++
		}
	}
	return n
}

// Start warms keep_warm tools and starts the idle sweeper.
func (p *Pool) Start() {
	p.once.Do(func() {
		upgrade.Register("tool plugin requests", p.Inflight)
		p.mu.Lock()
		for key, r := range p.runners {
			if r.keepWarm {
				go func(key string, r *Runner) {
					if err := r.Warm(); err != nil {
						log.Warn().Str("tool", key).Err(err).Msg("tool plugin: keep_warm start failed")
					}
				}(key, r)
			}
		}
		p.mu.Unlock()
		go func() {
			interval := p.ttl / 2
			if interval < time.Second {
				interval = time.Second
			}
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					p.Sweep()
				case <-p.stop:
					return
				}
			}
		}()
	})
}

// KillAll stops the sweeper and every process (app shutdown).
func (p *Pool) KillAll() {
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.runners {
		r.Stop()
	}
}

var methods = []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

// BuildModule turns one discovered kind=tool plugin into a tool.Module whose
// routes proxy to the plugin, and registers its runner with the pool.
func (p *Pool) BuildModule(f connplugin.Found) tool.Module {
	return p.buildModule(f, spawnProcess, connplugin.RunDir())
}

func (p *Pool) buildModule(f connplugin.Found, spawn spawnFn, sockDir string) tool.Module {
	tm := f.Manifest.Tool
	if tm == nil {
		tm = &wickplugin.ToolModule{}
	}
	m := tm.Meta
	meta := tool.Tool{
		Key:               f.Key,
		Name:              m.Name,
		Description:       m.Description,
		Icon:              m.Icon,
		Category:          m.Category,
		ExternalURL:       m.ExternalURL,
		DefaultVisibility: entity.ToolVisibility(m.Visibility),
		DefaultTags:       m.DefaultTags,
		FullScreen:        m.FullScreen,
		Replaces:          m.Replaces,
	}
	if meta.Name == "" {
		meta.Name = f.Manifest.Module.Meta.Name
	}
	if meta.Icon == "" {
		meta.Icon = "🧩"
	}
	if meta.DefaultVisibility == "" {
		meta.DefaultVisibility = entity.VisibilityPrivate
	}
	keys := make([]string, 0, len(tm.Configs))
	for _, c := range tm.Configs {
		keys = append(keys, c.Key)
	}
	base := "/tools/" + f.Key
	runner := newRunner(f.Key, f.BinaryPath, sockDir, tm.KeepWarm, spawn)
	px := &Proxy{key: f.Key, base: base, version: f.Manifest.Version, runner: runner}
	for _, h := range tm.Webhooks {
		g := base + h.Group
		dup := false
		for _, x := range px.hookGroups {
			dup = dup || x == g
		}
		if !dup {
			px.hookGroups = append(px.hookGroups, g)
		}
	}
	p.add(f.Key, f.Manifest.Version, runner)
	register := func(r tool.Router) {
		h := func(c *tool.Ctx) { px.ServeCtx(c, keys) }
		for _, method := range methods {
			route(r, method, "/", h)
			route(r, method, "/{path...}", h)
		}
		groups := map[string]tool.WebhookRouter{}
		for _, wh := range tm.Webhooks {
			g := groups[wh.Group]
			if g == nil {
				g = r.WebhookGroup(wh.Group)
				groups[wh.Group] = g
			}
			hook(g, wh.Method, strings.TrimPrefix(wh.Path, wh.Group), func(c *tool.WebhookCtx) { px.ServeWebhook(c, keys) })
		}
	}
	return tool.Module{Meta: meta, Configs: tm.Configs, Register: register}
}

func route(r tool.Router, method, path string, h tool.HandlerFunc) {
	switch method {
	case "GET":
		r.GET(path, h)
	case "POST":
		r.POST(path, h)
	case "PUT":
		r.PUT(path, h)
	case "DELETE":
		r.DELETE(path, h)
	case "PATCH":
		r.PATCH(path, h)
	}
}

func hook(g tool.WebhookRouter, method, path string, h tool.WebhookHandlerFunc) {
	switch method {
	case "GET":
		g.GET(path, h)
	case "POST":
		g.POST(path, h)
	case "PUT":
		g.PUT(path, h)
	case "DELETE":
		g.DELETE(path, h)
	case "PATCH":
		g.PATCH(path, h)
	}
}

// Load registers every enabled, verified kind=tool plugin under dir with
// register (tools.Register) and returns how many were registered. Call it
// before tools.All() so the plugin's config rows are seeded like a built-in
// tool's. record (optional) is told the kind + version of each plugin.
func (p *Pool) Load(dir string, enabled func(string) bool, record func(key, kind, version string) error, register func(tool.Module)) int {
	return p.load(dir, enabled, record, register, p.BuildModule)
}

func (p *Pool) load(dir string, enabled func(string) bool, record func(key, kind, version string) error,
	register func(tool.Module), build func(connplugin.Found) tool.Module) int {
	found, err := connplugin.ScanKind(dir, wickplugin.KindTool)
	if err != nil {
		log.Warn().Err(err).Msg("tool plugins: scan failed")
		return 0
	}
	n := 0
	for _, f := range found {
		if err := wickplugin.ValidateKey(f.Key); err != nil {
			log.Warn().Str("plugin", f.Key).Err(err).Msg("tool plugin: skipped (invalid key)")
			continue
		}
		if enabled != nil && !enabled(f.Key) {
			continue
		}
		if err := wickplugin.VerifyManifest(f.Manifest, f.BinaryPath); err != nil {
			log.Warn().Str("tool", f.Key).Err(err).Msg("tool plugin: skipped (verification failed)")
			continue
		}
		register(build(f))
		if record != nil {
			if err := record(f.Key, wickplugin.KindTool, f.Manifest.Version); err != nil {
				log.Warn().Str("tool", f.Key).Err(err).Msg("tool plugin: state record failed")
			}
		}
		n++
	}
	return n
}

// rawHTML is a templ component writing trusted plugin HTML verbatim (the
// plugin is installed and verified by an admin, like any built-in tool).
func rawHTML(b []byte) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := w.Write(b)
		return err
	})
}
