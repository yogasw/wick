package plugin

import (
	"fmt"
	"sort"
	"time"

	"github.com/rs/zerolog/log"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// reconcileInterval is how often the plugin folder is rescanned, matching the
// connector plugin reloader: an install from the CLI lands within it.
const reconcileInterval = 5 * time.Second

// Sink is what a running wick does with a tool plugin the pool loads or
// unloads: mount / drop its routes, add it to / remove it from the tool
// registry, seed its config rows and default tags. Add returning an error
// leaves the plugin unloaded, with the error shown as its status.
type Sink interface {
	Add(m tool.Module) error
	Remove(key string)
}

// bootSink is the boot-time Sink: before routes exist, loading a plugin only
// means registering its module so the boot passes (config bootstrap, route
// mount) treat it like a built-in. validate is the live Sink's check, so a
// plugin the running process would refuse is refused at boot too — as an
// Error status, never a failed boot.
type bootSink struct {
	register func(tool.Module)
	validate func(tool.Module) error
}

func (b bootSink) Add(m tool.Module) error {
	if b.validate != nil {
		if err := b.validate(m); err != nil {
			return err
		}
	}
	if b.register != nil {
		b.register(m)
	}
	return nil
}

func (bootSink) Remove(string) {}

// loadedTool is one tool plugin the running process serves: the manifest
// hash it was built from (a changed hash means an update) and its module.
type loadedTool struct {
	sha string
	mod tool.Module
}

// Attach hands the pool the live Sink and makes every later Reconcile
// (watcher tick, install, enable, disable) act on the running process.
// Plugins loaded at boot are already registered, so Attach only remembers
// the sink — mount those through Modules.
func (p *Pool) Attach(s Sink) {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	p.sink = s
}

// Reconcile rescans the tool plugin folder and brings the running process in
// line with it: a new or updated plugin is loaded (the old version's process
// stopped first), a disabled or removed one is unloaded, and a plugin that
// fails to load records why. A no-op until Load and Attach ran.
func (p *Pool) Reconcile() {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	if p.sink == nil || p.dir == "" {
		return
	}
	p.reconcile(p.sink)
}

func (p *Pool) watch(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			p.Reconcile()
		case <-p.stop:
			return
		}
	}
}

// reconcile is one pass; the caller holds rmu. Returns how many plugins are
// loaded after it.
func (p *Pool) reconcile(sink Sink) int {
	found, err := connplugin.ScanKind(p.dir, wickplugin.KindTool)
	if err != nil {
		log.Warn().Err(err).Msg("tool plugins: scan failed")
		return p.loadedCount()
	}
	present := map[string]bool{}
	for _, f := range found {
		present[f.Key] = true
		p.mu.Lock()
		removing := p.removing[f.Key]
		p.seen[f.Key] = true
		p.mu.Unlock()
		if removing {
			continue
		}
		if err := wickplugin.ValidateKey(f.Key); err != nil {
			log.Warn().Str("plugin", f.Key).Err(err).Msg("tool plugin: skipped (invalid key)")
			p.setErr(f.Key, "invalid key: "+err.Error())
			continue
		}
		on, err := true, error(nil)
		if p.enabled != nil {
			on, err = p.enabled(f.Key)
		}
		if err != nil {
			// The stored flag could not be read: keep what is running (or
			// not running) rather than flip a disabled plugin back on.
			log.Warn().Str("tool", f.Key).Err(err).Msg("tool plugin: enable state unreadable, keeping current state")
			continue
		}
		if !on {
			if p.unloadWith(f.Key, sink) {
				log.Info().Str("tool", f.Key).Msg("tool plugin: disabled, unloaded")
			}
			p.setErr(f.Key, "")
			continue
		}
		if sha, ok := p.loadedSHA(f.Key); ok && sha == f.Manifest.SHA256 {
			continue
		}
		// The binary is replaced in place, so a version that fails to verify
		// must not stay mounted: its next spawn would run the new file.
		if err := wickplugin.VerifyManifest(f.Manifest, f.BinaryPath); err != nil {
			log.Warn().Str("tool", f.Key).Err(err).Msg("tool plugin: skipped (verification failed)")
			p.unloadWith(f.Key, sink)
			p.setErr(f.Key, "verification failed: "+err.Error())
			continue
		}
		updated := p.unloadWith(f.Key, sink)
		m := p.build(f)
		if err := sink.Add(m); err != nil {
			log.Warn().Str("tool", f.Key).Err(err).Msg("tool plugin: load failed")
			p.stopRunner(f.Key)
			p.setErr(f.Key, err.Error())
			continue
		}
		p.mu.Lock()
		p.loaded[f.Key] = loadedTool{sha: f.Manifest.SHA256, mod: m}
		delete(p.errs, f.Key)
		p.mu.Unlock()
		if p.record != nil {
			if err := p.record(f.Key, wickplugin.KindTool, f.Manifest.Version); err != nil {
				log.Warn().Str("tool", f.Key).Err(err).Msg("tool plugin: state record failed")
			}
		}
		if p.sink != nil {
			p.warmIfKeepWarm(f.Key)
			verb := "loaded"
			if updated {
				verb = "updated"
			}
			log.Info().Str("tool", f.Key).Str("version", f.Manifest.Version).Msg("tool plugin: " + verb + " live")
		}
	}
	p.mu.Lock()
	var gone []string
	for key := range p.loaded {
		if !present[key] {
			gone = append(gone, key)
		}
	}
	for key := range p.errs {
		if !present[key] {
			delete(p.errs, key)
		}
	}
	for key := range p.removing {
		if !present[key] {
			delete(p.removing, key)
		}
	}
	for key := range p.seen {
		if !present[key] {
			delete(p.seen, key)
		}
	}
	p.mu.Unlock()
	for _, key := range gone {
		p.unloadWith(key, sink)
		log.Info().Str("tool", key).Msg("tool plugin: removed, unloaded")
	}
	return p.loadedCount()
}

// unload drops tool plugin key through the attached sink; the caller holds rmu.
func (p *Pool) unload(key string) bool { return p.unloadWith(key, p.sink) }

// unloadWith takes key out of the running process: first its routes and
// registry entry (through sink, when the pool had it loaded — a key the pool
// never loaded is left alone, it may belong to a built-in tool), then its
// runner is closed so requests already in flight drain (bounded by
// DrainGrace) before the process is killed. Reports whether it was loaded.
func (p *Pool) unloadWith(key string, sink Sink) bool {
	p.mu.Lock()
	_, ok := p.loaded[key]
	delete(p.loaded, key)
	p.mu.Unlock()
	if ok && sink != nil {
		sink.Remove(key)
	}
	p.stopRunner(key)
	return ok
}

func (p *Pool) setErr(key, msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if msg == "" {
		delete(p.errs, key)
		return
	}
	p.errs[key] = msg
}

func (p *Pool) loadedSHA(key string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.loaded[key]
	return l.sha, ok
}

func (p *Pool) loadedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.loaded)
}

// Owns reports whether key is a tool plugin the running process has loaded.
func (p *Pool) Owns(key string) bool {
	if p == nil {
		return false
	}
	_, ok := p.loadedSHA(key)
	return ok
}

// Modules returns the loaded tool plugin modules, sorted by key.
func (p *Pool) Modules() []tool.Module {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]tool.Module, 0, len(p.loaded))
	for _, l := range p.loaded {
		out = append(out, l.mod)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.Key < out[j].Meta.Key })
	return out
}

// Load states reported by State.
const (
	StateActive  = "active"  // loaded and routable
	StateError   = "error"   // the last load failed; reason says why
	StateIdle    = "idle"    // seen and deliberately not loaded (disabled, state unreadable)
	StatePending = "pending" // installed but no pass has looked at it yet
)

// State reports what the running process does with tool plugin key, and for
// StateError why the last load failed.
func (p *Pool) State(key string) (state, reason string) {
	if p == nil {
		return StatePending, ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.loaded[key]; ok {
		return StateActive, ""
	}
	if e := p.errs[key]; e != "" {
		return StateError, e
	}
	if p.seen[key] {
		return StateIdle, ""
	}
	return StatePending, ""
}

// warmIfKeepWarm spawns a keep_warm tool loaded after Start (Start only warms
// the ones present at boot).
func (p *Pool) warmIfKeepWarm(key string) {
	p.mu.Lock()
	r := p.runners[key]
	started := p.started
	p.mu.Unlock()
	if r == nil || !r.keepWarm || !started {
		return
	}
	go func() {
		if err := r.Warm(); err != nil {
			log.Warn().Str("tool", key).Err(err).Msg("tool plugin: keep_warm start failed")
		}
	}()
}

// ErrKeyTaken is what a Sink returns when a plugin's key belongs to a tool
// the plugin does not own (a built-in).
func ErrKeyTaken(key string) error {
	return fmt.Errorf("key %q is already used by a built-in tool", key)
}

// Reject unloads tool plugin key (no sink: the caller already took it out of
// the registry) and shows err as its status — for a plugin boot accepted
// but could not mount.
func (p *Pool) Reject(key string, err error) {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	p.unloadWith(key, nil)
	p.setErr(key, err.Error())
}
