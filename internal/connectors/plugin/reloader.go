package plugin

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// moduleSink is the subset of *connectors.Service the reloader needs. Defined
// here (consumer side) so this package does not import the service package
// directly — *connectors.Service satisfies it structurally.
type moduleSink interface {
	UpsertModule(ctx context.Context, m connector.Module) error
	RemoveModule(key string)
}

// enabledChecker is the subset of *StateStore the reloader needs. nil means
// every plugin is treated as enabled.
type enabledChecker interface {
	Enabled(key string) bool
}

// Reloader watches the plugins dir and reconciles installed plugins into the
// running service without a restart. It polls on a fixed interval (no fsnotify
// dependency).
type Reloader struct {
	dir      string
	svc      moduleSink
	mgr      *Manager
	interval time.Duration
	seen     map[string]string // key -> sha256
	stop     chan struct{}
	store    enabledChecker
}

// NewReloader builds a Reloader. interval <= 0 defaults to 5s.
func NewReloader(dir string, svc moduleSink, mgr *Manager, interval time.Duration, store enabledChecker) *Reloader {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Reloader{
		dir:      dir,
		svc:      svc,
		mgr:      mgr,
		interval: interval,
		seen:     map[string]string{},
		stop:     make(chan struct{}),
		store:    store,
	}
}

// Start runs the poll loop until Stop is called or ctx is cancelled. Call once.
func (r *Reloader) Start(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.reconcile(ctx)
		case <-r.stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Reload triggers an immediate reconcile (for in-process callers, e.g. CLI install).
func (r *Reloader) Reload(ctx context.Context) { r.reconcile(ctx) }

// Stop ends the poll loop.
func (r *Reloader) Stop() { close(r.stop) }

func (r *Reloader) reconcile(ctx context.Context) {
	found, err := Scan(r.dir)
	if err != nil {
		log.Warn().Err(err).Msg("connector plugin reload: scan failed")
		return
	}
	present := map[string]bool{}
	for _, f := range found {
		if r.store != nil && !r.store.Enabled(f.Key) {
			if _, ok := r.seen[f.Key]; ok {
				r.svc.RemoveModule(f.Key)
				r.mgr.RemoveBinary(f.Key)
				delete(r.seen, f.Key)
				log.Info().Str("connector", f.Key).Msg("connector plugin reload: disabled, removed")
			}
			continue
		}
		present[f.Key] = true
		if r.seen[f.Key] == f.Manifest.SHA256 {
			continue
		}
		if err := wickplugin.VerifyManifest(f.Manifest, f.BinaryPath); err != nil {
			log.Warn().Str("connector", f.Key).Err(err).Msg("connector plugin reload: skipped (verification failed)")
			continue
		}
		mod := BuildModule(f.Manifest.Module, r.mgr.Client)
		if err := r.svc.UpsertModule(ctx, mod); err != nil {
			log.Warn().Str("connector", f.Key).Err(err).Msg("connector plugin reload: upsert failed")
			continue
		}
		r.mgr.SetBinary(f.Key, f.BinaryPath)
		r.seen[f.Key] = f.Manifest.SHA256
		log.Info().Str("connector", f.Key).Str("version", f.Manifest.Version).Msg("connector plugin reload: registered")
	}
	for key := range r.seen {
		if !present[key] {
			r.svc.RemoveModule(key)
			r.mgr.RemoveBinary(key)
			delete(r.seen, key)
			log.Info().Str("connector", key).Msg("connector plugin reload: removed")
		}
	}
}
