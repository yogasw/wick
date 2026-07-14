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
	"maps"
	"net/http"
	"strings"
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

	mu           sync.Mutex
	channels     []Channel
	sources      map[string]ConfigSource // by Channel.Name()
	instanceKeys map[Channel]string

	// turns tracks the per-session in-flight turn so a reply the agent opens
	// with the [silent] marker is kept out of every channel AND out of the
	// idle push notification, while still flowing to the web UI. See turnState
	// + DispatchAgentEvent.
	turns map[string]*turnState

	// silentLast records, per session, whether the most recently COMPLETED
	// turn was silent. dispatchLifecyclePush reads it so a silent turn's reply
	// never becomes a push alert. Set when a turn ends silent; cleared when a
	// non-silent turn ends.
	silentLast map[string]bool
}

// SilentMarker is the case-insensitive prefix an agent puts at the very start
// of a reply to keep that reply out of every channel and push notification.
// The message still reaches the web UI (flagged, rendered muted) and the
// conversation log — it just isn't pushed outward. Used for "work quietly,
// don't ping the user unless it matters" turns (e.g. a monitor loop).
const SilentMarker = "[silent]"

// silentDecideMin is how many bytes of the reply we buffer before deciding
// whether it opens with SilentMarker, so a marker split across stream deltas
// ("[si" then "lent]") is still detected. Any of: this many bytes, a newline,
// or the turn ending forces the decision.
const silentDecideMin = len(SilentMarker)

// turnState buffers a session's streamed reply until we can tell whether it is
// silent. Once decided, events either flush to channels (normal) or are
// dropped (silent). Only TextDelta is buffered pre-decision; status-only
// events (Thinking/ToolUse/ToolResult) are held too so they don't leak a
// silent turn's activity to channels before the marker is seen.
type turnState struct {
	buf      strings.Builder     // accumulated reply text, pre-decision
	pending  []event.AgentEvent  // events held until the silent decision
	decided  bool
	silent   bool
}

// NewRegistry returns an empty registry. Use the With* methods to attach
// shared dependencies before calling Add.
func NewRegistry() *Registry {
	return &Registry{
		sources:      map[string]ConfigSource{},
		instanceKeys: map[Channel]string{},
		turns:        map[string]*turnState{},
		silentLast:   map[string]bool{},
	}
}

// MarkSilent forces a session's next turn silent regardless of its text — for
// callers that KNOW a turn should stay off-channel before it starts (a
// system-initiated turn). Content-based [silent] detection is the usual path;
// this is the explicit override.
func (r *Registry) MarkSilent(sessionID string) {
	r.mu.Lock()
	ts := r.turns[sessionID]
	if ts == nil {
		ts = &turnState{}
		r.turns[sessionID] = ts
	}
	ts.decided = true
	ts.silent = true
	r.mu.Unlock()
}

// ClearSilent drops any in-flight turn state for a session — e.g. a caller
// that MarkSilent'd a turn that never started, so the next real turn isn't
// wrongly suppressed.
func (r *Registry) ClearSilent(sessionID string) {
	r.mu.Lock()
	delete(r.turns, sessionID)
	delete(r.silentLast, sessionID)
	r.mu.Unlock()
}

// WasLastTurnSilent reports whether the session's most recently completed turn
// was silent. dispatchLifecyclePush uses it to skip the "your turn is back"
// alert for a silent reply.
func (r *Registry) WasLastTurnSilent(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.silentLast[sessionID]
}

// looksSilent reports whether accumulated reply text opens with SilentMarker,
// ignoring leading whitespace and case.
func looksSilent(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimLeft(s, " \t\r\n")), SilentMarker)
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

// AddKeyed registers a channel under an explicit instanceKey (e.g. "slack:user-abc")
// instead of c.Name(). Use when multiple instances of the same channel type coexist.
func (r *Registry) AddKeyed(instanceKey string, c Channel, src ConfigSource) {
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
	r.instanceKeys[c] = instanceKey
	if src != nil {
		r.sources[instanceKey] = src
	}
	r.mu.Unlock()
}

// RemoveKeyed stops and removes the channel registered under instanceKey.
func (r *Registry) RemoveKeyed(instanceKey string) {
	r.mu.Lock()
	var target Channel
	for _, c := range r.channels {
		if r.instanceKeys[c] == instanceKey {
			target = c
			break
		}
	}
	if target == nil {
		r.mu.Unlock()
		return
	}
	newChannels := make([]Channel, 0, len(r.channels)-1)
	for _, c := range r.channels {
		if c != target {
			newChannels = append(newChannels, c)
		}
	}
	r.channels = newChannels
	delete(r.instanceKeys, target)
	delete(r.sources, instanceKey)
	r.mu.Unlock()
	target.Stop()
}

// HasKey reports whether an instance with the given key is registered.
func (r *Registry) HasKey(instanceKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sources[instanceKey]
	return ok
}

// HasAnyKeyed reports whether any instance registered under the per-user
// keyed scheme (key = "<channelType>:<something>") exists for the given
// channelType prefix. Used to distinguish per-user channel types (slack)
// from single-instance types (telegram, rest) when deciding whether to
// fall back to ChannelByName for unconfigured users.
func (r *Registry) HasAnyKeyed(channelType string) bool {
	prefix := channelType + ":"
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.sources {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// ChannelByKey returns the channel registered under instanceKey, or nil.
func (r *Registry) ChannelByKey(instanceKey string) Channel {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c, k := range r.instanceKeys {
		if k == instanceKey {
			return c
		}
	}
	return nil
}

// SendFuncFor returns the registry's shared sendFn (used when dynamically adding channels).
func (r *Registry) SendFuncFor(_ string) SendFunc {
	return r.sendFn
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

// candidate pairs an HTTP handler with the channel instance that produced
// it, so a fan-in dispatcher can ask the instance whether a request is
// destined for it (RequestRouter).
type candidate struct {
	ch Channel
	h  http.Handler
}

// HTTPHandlers returns the webhook handlers exposed by channels that
// implement HTTPHandlerProvider. Caller mounts them on the public mux.
//
// When several keyed instances of the same channel type expose the same
// route (e.g. two per-user Slack bots both mounting
// /integrations/slack/send), they are merged into one fan-in dispatcher
// instead of the previous last-write-wins map merge — which silently
// dropped every instance but the last. The dispatcher routes each request
// to the instance that claims it via RequestRouter.OwnsRequest, falling
// back to the first registered instance when none claims it.
func (r *Registry) HTTPHandlers() map[string]http.Handler {
	byPath := map[string][]candidate{}
	for _, c := range r.Channels() {
		if h, ok := c.(MultiHTTPHandlerProvider); ok {
			for path, hh := range h.HTTPHandlers() {
				byPath[path] = append(byPath[path], candidate{ch: c, h: hh})
			}
		} else if h, ok := c.(HTTPHandlerProvider); ok {
			byPath[h.HTTPPath()] = append(byPath[h.HTTPPath()], candidate{ch: c, h: h.HTTPHandler()})
		}
	}

	out := make(map[string]http.Handler, len(byPath))
	for path, cands := range byPath {
		if len(cands) == 1 {
			out[path] = cands[0].h
			continue
		}
		log.Info().Str("path", path).Int("instances", len(cands)).
			Msg("multiple channel instances on one route; mounting fan-in dispatcher")
		out[path] = fanInHandler(cands)
	}
	return out
}

// fanInHandler routes a request to the first candidate whose channel
// claims it via RequestRouter.OwnsRequest. Candidates that do not
// implement RequestRouter (or when none claim) act as a fallback: the
// first such candidate, else the first candidate overall, serves it.
func fanInHandler(cands []candidate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for _, c := range cands {
			if rr, ok := c.ch.(RequestRouter); ok && rr.OwnsRequest(req) {
				c.h.ServeHTTP(w, req)
				return
			}
		}
		// No instance explicitly claimed it — fall back to the first
		// non-RequestRouter (catch-all) instance, else the first overall.
		for _, c := range cands {
			if _, ok := c.ch.(RequestRouter); !ok {
				c.h.ServeHTTP(w, req)
				return
			}
		}
		cands[0].h.ServeHTTP(w, req)
	})
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
// implements AgentEventReceiver. Channels filter by sessionID internally
// (events for sessions they didn't originate are ignored).
//
// Silent-reply handling: a reply the agent opens with [silent] must not reach
// any channel. But replies stream in deltas, so we can't know at the first
// event — instead we buffer the turn's events until we've seen enough text to
// decide (silentDecideMin bytes, a newline, or the turn ending). Until then no
// event is forwarded. Once decided:
//   - not silent → flush every held event to channels, then pass through live.
//   - silent     → drop everything; nothing reaches any channel this turn.
// The web-UI/SSE path is separate (the pool's OnEvent → broadcaster) and is
// never gated here, so the reply still shows in the conversation view. The
// turn's silent verdict is recorded (silentLast) so the idle push alert can be
// skipped too. State is per-session and reset on the terminal event.
func (r *Registry) DispatchAgentEvent(sessionID string, ev event.AgentEvent) {
	r.mu.Lock()
	ts := r.turns[sessionID]
	if ts == nil {
		ts = &turnState{}
		r.turns[sessionID] = ts
	}

	terminal := ev.Type == event.Done || ev.Type == event.Error

	// Pre-decision: accumulate text, hold events, decide when we can.
	if !ts.decided {
		if ev.Type == event.TextDelta {
			ts.buf.WriteString(ev.Text)
		}
		ts.pending = append(ts.pending, ev)

		text := ts.buf.String()
		canDecide := terminal || len(text) >= silentDecideMin || strings.ContainsAny(text, "\n")
		if !canDecide {
			r.mu.Unlock()
			return // keep buffering
		}
		ts.decided = true
		ts.silent = looksSilent(text)
		held := ts.pending
		ts.pending = nil
		silent := ts.silent
		if terminal {
			r.finishTurnLocked(sessionID, ts)
		}
		r.mu.Unlock()
		if silent {
			return // drop the whole held batch — nothing goes to channels
		}
		r.forward(sessionID, held) // flush buffered events in order
		return
	}

	// Post-decision.
	silent := ts.silent
	if terminal {
		r.finishTurnLocked(sessionID, ts)
	}
	r.mu.Unlock()
	if silent {
		return
	}
	r.forward(sessionID, []event.AgentEvent{ev})
}

// finishTurnLocked records the turn's silent verdict for the idle-push check
// and clears the in-flight state. Caller holds r.mu.
func (r *Registry) finishTurnLocked(sessionID string, ts *turnState) {
	r.silentLast[sessionID] = ts.silent
	delete(r.turns, sessionID)
}

// forward fans a batch of events out to every AgentEventReceiver channel.
func (r *Registry) forward(sessionID string, evs []event.AgentEvent) {
	channels := r.Channels()
	for _, ev := range evs {
		for _, c := range channels {
			if x, ok := c.(AgentEventReceiver); ok {
				x.OnAgentEvent(sessionID, ev)
			}
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
			maps.Copy(snap, r.sources)
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
