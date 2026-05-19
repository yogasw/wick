// Package channels — Registry: central wiring + fan-out for transports.
//
// Registry is the single seam between the server and individual Channel
// implementations. Server constructs each channel with its config (and
// nothing else), hands it to the registry via Add, and the registry
// auto-wires shared dependencies through optional setter interfaces:
// SendFunc, SessionChecker, SessionStartHook, ApproveFn, PublicURL.
//
// Event fan-out (DispatchAgentEvent, DispatchApproval*) iterates the
// registered channels and forwards to those that implement the matching
// receiver interface. Channels that don't care (UI, API) skip cleanly.
//
// Hot-reload is generic via ConfigSource: each channel registers a
// (Hash, Reload) pair; WatchConfigs polls every interval and triggers
// Reload only when the hash changes. Channel implementations own their
// own DB reads.

package channels

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
)

// Registry holds the shared dependency set every channel may need plus
// the channel list itself. Construct via NewRegistry, attach deps via
// the With* setters, then Add channels and Start.
type Registry struct {
	sendFn         SendFunc
	sessionChecker SessionChecker
	sessionHook    SessionStartHook
	approveFn      RegistryApproveFn
	publicURL      string

	mu       sync.Mutex
	channels []Channel
	sources  map[string]ConfigSource // by Channel.Name()
}

// NewRegistry returns an empty registry. Use the With* methods to attach
// shared dependencies before calling Add.
func NewRegistry() *Registry {
	return &Registry{sources: map[string]ConfigSource{}}
}

// WithSendFunc attaches the pool dispatch closure. Called once at boot.
func (r *Registry) WithSendFunc(fn SendFunc) *Registry { r.sendFn = fn; return r }

// WithSessionChecker attaches the session-exists probe.
func (r *Registry) WithSessionChecker(c SessionChecker) *Registry { r.sessionChecker = c; return r }

// WithSessionStartHook attaches the new-session notifier.
func (r *Registry) WithSessionStartHook(h SessionStartHook) *Registry {
	r.sessionHook = h
	return r
}

// WithApproveFn attaches the gate approval resolver. The signature
// includes channelName so the manager can record which transport
// posted the decision; Add binds each channel's name into a 4-arg
// ApproveFn before handing it to the channel's setter.
func (r *Registry) WithApproveFn(fn RegistryApproveFn) *Registry { r.approveFn = fn; return r }

// WithPublicURL attaches the public base URL for dashboard links.
func (r *Registry) WithPublicURL(u string) *Registry { r.publicURL = u; return r }

// Add registers a channel and auto-wires every shared dependency the
// channel implements via its setter interface. Idempotent across
// dependencies — if a setter is missing the channel just skips that
// wire.
//
// Pass an optional ConfigSource to enable hot-reload for the channel.
// Nil source = channel never reloaded by WatchConfigs.
func (r *Registry) Add(c Channel, src ConfigSource) {
	if c == nil {
		return
	}
	if r.sendFn != nil {
		if x, ok := c.(SendFuncSetter); ok {
			x.SetSendFunc(r.sendFn)
		}
	}
	if r.sessionChecker != nil {
		if x, ok := c.(SessionCheckerSetter); ok {
			x.SetSessionChecker(r.sessionChecker)
		}
	}
	if r.sessionHook != nil {
		if x, ok := c.(SessionStartHookSetter); ok {
			x.SetSessionStartHook(r.sessionHook)
		}
	}
	if r.approveFn != nil {
		if x, ok := c.(ApproveFnSetter); ok {
			name := c.Name()
			fn := r.approveFn
			x.SetApproveFn(func(sid, rid, decision, matchKey string) error {
				return fn(name, sid, rid, decision, matchKey)
			})
		}
	}
	if r.publicURL != "" {
		if x, ok := c.(PublicURLSetter); ok {
			x.SetPublicURL(r.publicURL)
		}
	}

	r.mu.Lock()
	r.channels = append(r.channels, c)
	if src != nil {
		r.sources[c.Name()] = src
	}
	r.mu.Unlock()
}

// ChannelByName returns the registered channel matching name, or nil.
func (r *Registry) ChannelByName(name string) Channel {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.channels {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// Channels returns a snapshot of the registered channels.
func (r *Registry) Channels() []Channel {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Channel, len(r.channels))
	copy(out, r.channels)
	return out
}

// HTTPHandlers returns the webhook handlers exposed by channels that
// implement HTTPHandlerProvider. Caller mounts them on the public mux.
func (r *Registry) HTTPHandlers() map[string]http.Handler {
	out := map[string]http.Handler{}
	for _, c := range r.Channels() {
		if h, ok := c.(MultiHTTPHandlerProvider); ok {
			for path, handler := range h.HTTPHandlers() {
				out[path] = handler
			}
		} else if h, ok := c.(HTTPHandlerProvider); ok {
			out[h.HTTPPath()] = h.HTTPHandler()
		}
	}
	return out
}

// StartAll starts every configured channel in its own goroutine.
// Unconfigured channels are skipped with an info log so operators see
// why their channel isn't live. Errors from Start are logged but
// non-fatal — one bad channel doesn't take down the rest.
func (r *Registry) StartAll(ctx context.Context) {
	for _, c := range r.Channels() {
		if !c.IsConfigured() {
			log.Info().Str("channel", c.Name()).Msg("not configured, skipping start")
			continue
		}
		ch := c // capture
		go func() {
			if err := ch.Start(ctx); err != nil {
				log.Error().Str("channel", ch.Name()).Err(err).Msg("channel stopped")
			}
		}()
	}
}

// StopAll signals every channel to shut down. Safe to call before any
// Start has returned — channels' Stop is expected to be idempotent.
func (r *Registry) StopAll() {
	for _, c := range r.Channels() {
		c.Stop()
	}
}

// DispatchAgentEvent fans out one agent event to every channel that
// implements AgentEventReceiver. Channels filter by sessionID
// internally (events for sessions they didn't originate are ignored).
func (r *Registry) DispatchAgentEvent(sessionID string, ev event.AgentEvent) {
	for _, c := range r.Channels() {
		if x, ok := c.(AgentEventReceiver); ok {
			x.OnAgentEvent(sessionID, ev)
		}
	}
}

// DispatchApprovalRequest fans out an approval request to every channel
// that implements ApprovalReceiver. The channel decides whether the
// request belongs to one of its sessions (by checking its session table).
func (r *Registry) DispatchApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	for _, c := range r.Channels() {
		if x, ok := c.(ApprovalReceiver); ok {
			x.OnApprovalRequest(sessionID, req)
		}
	}
}

// DispatchApprovalResolved fans out an approval-resolved notification.
func (r *Registry) DispatchApprovalResolved(sessionID, requestID, decision string) {
	for _, c := range r.Channels() {
		if x, ok := c.(ApprovalReceiver); ok {
			x.OnApprovalResolved(sessionID, requestID, decision)
		}
	}
}

// WatchConfigs polls every registered ConfigSource at the given interval
// and triggers Reload when its Hash changes. Blocks until ctx is done.
// Run in its own goroutine.
func (r *Registry) WatchConfigs(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	hashes := map[string]string{}
	r.mu.Lock()
	for name, src := range r.sources {
		hashes[name] = src.Hash()
	}
	r.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			snap := make(map[string]ConfigSource, len(r.sources))
			for k, v := range r.sources {
				snap[k] = v
			}
			r.mu.Unlock()

			for name, src := range snap {
				newHash := src.Hash()
				if newHash == hashes[name] {
					continue
				}
				hashes[name] = newHash
				log.Info().Str("channel", name).Msg("config changed, hot-reloading")
				if err := src.Reload(ctx); err != nil {
					log.Warn().Str("channel", name).Err(err).Msg("reload failed")
				}
			}
		}
	}
}
