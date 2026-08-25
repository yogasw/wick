// Package integration is the workflow-side surface for per-event +
// per-action node descriptors. Each channel registers its events and
// actions as individual descriptors at boot time; the editor palette
// and the engine both consume the registry.
//
// Why this exists separately from agents/channels.Registry:
//
//   - channels.Registry is the *transport* registry — one entry per
//     channel binary (Slack, Telegram, REST). It owns lifecycle, hot
//     reload, HTTP webhook handlers, agent session fan-out.
//
//   - integration.Registry is the *workflow surface* — one entry per
//     event class ("slack.message", "slack.block_action") and one per
//     action ("slack.send_message", "slack.open_modal"). Adding a new
//     event or action does not require changing the channel transport
//     or the engine — just register a new descriptor.
//
// Pattern mirrors pkg/connector.Module → connector.Registry: schema is
// declared in code, the LLM and the palette read the registry, no hand
// rolled tables anywhere.
package integration

import (
	"context"
	"sort"
	"sync"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// EventDescriptor declares one inbound event class a channel can fire
// as a workflow trigger. The (Channel, Event) pair is the canonical
// key — e.g. ("slack", "message"), ("slack", "block_action"),
// ("telegram", "callback_query").
//
// PayloadType is a zero-value sample struct used for two purposes:
//   - schema generation for the editor's payload picker
//   - documenting the keys downstream nodes can reference via
//     `{{.Event.Payload.<key>}}` template expansion
//
// Channels emit events as map[string]any using the keys declared on
// PayloadType so the editor and the engine see a stable shape.
//
// MatchSchema declares per-event filter fields the trigger inspector
// renders as a form (channel id whitelist, user filter, text contains,
// etc.). Each event owns its own schema since match keys are
// channel-specific. The router reads stored values from Trigger.Match
// and skips the run when a non-empty field doesn't match the event
// payload.
//
// Build via entity.StructToConfigs(MyMatchStruct{}). Empty schema =
// no filter UI for this event = the trigger always fires (dump-all).
//
// Match evaluation is per-descriptor — supply a MatchFunc when the
// default key-equality semantics don't fit (regex, set membership,
// custom transforms). When MatchFunc is nil, the router falls back
// to a generic "for each spec key, payload[key] must equal spec
// value (unless spec is empty)" comparison.
type MatchFunc func(spec map[string]any, payload map[string]any) bool

// EventDescriptor declares one inbound event class a channel can fire
// as a workflow trigger. The (Channel, Event) pair is the canonical
// key — e.g. ("slack", "message"), ("slack", "block_action"),
// ("telegram", "callback_query").
//
// PayloadType is a zero-value sample struct used for two purposes:
//   - schema generation for the editor's payload picker
//   - documenting the keys downstream nodes can reference via
//     `{{.Event.Payload.<key>}}` template expansion
//
// MatchSchema declares the trigger filter form. Operators set values
// in the inspector → stored under Trigger.Match → router applies them
// at dispatch time.
type EventDescriptor struct {
	Channel     string          // "slack" | "telegram" | …
	Event       string          // "message" | "block_action" | …
	Name        string          // UI label: "Slack: New message"
	Description string          // one-liner shown in palette
	PayloadType any             // zero-value sample for schema gen
	MatchSchema []entity.Config // filter form schema (per event)
	Match       MatchFunc       // optional custom matcher; nil = key-equality
	wickdocs.Docs               // opt-in self-documentation for MCP workflow_node_detail
}

// Key returns the canonical "<channel>.<event>" identifier the workflow
// trigger references via `event_key:` or the legacy
// (channel:<name>, event:<name>) pair.
func (e EventDescriptor) Key() string { return e.Channel + "." + e.Event }

// ActionDescriptor declares one outbound op a workflow action node can
// invoke. Same model as connector.Op — typed input/output for schema
// gen, generic map-based Execute so the engine doesn't need per-type
// reflection.
type ActionDescriptor struct {
	Channel     string      // "slack" | "telegram" | …
	Action      string      // "send_message" | "open_modal" | …
	Name        string      // UI label: "Slack: Send message"
	Description string      // one-liner shown in palette
	InputType   any         // zero-value sample for input schema
	OutputType  any         // zero-value sample for output schema
	Destructive bool        // shown with the destructive badge in UI
	Execute     ExecuteFunc // handler — receives args, returns output
	wickdocs.Docs           // opt-in self-documentation for MCP workflow_node_detail
}

// Key returns the canonical "<channel>.<action>" identifier.
func (a ActionDescriptor) Key() string { return a.Channel + "." + a.Action }

// ExecuteFunc is the action handler signature. args carries the typed
// input encoded as a generic map (matches the workflow YAML / canvas
// inspector shape). Implementations decode args themselves; helpers
// for required-key extraction live in this package.
type ExecuteFunc func(ctx context.Context, args map[string]any) (any, error)

// Registry holds every registered event + action. One instance per
// wick process — typically constructed in workflow setup and handed to
// each channel's RegisterAll function.
type Registry struct {
	mu      sync.RWMutex
	events  map[string]EventDescriptor
	actions map[string]ActionDescriptor
}

// New constructs an empty Registry.
func New() *Registry {
	return &Registry{
		events:  map[string]EventDescriptor{},
		actions: map[string]ActionDescriptor{},
	}
}

// RegisterEvent adds an event descriptor. Re-registering the same key
// silently overwrites — useful for test fakes; production paths must
// register exactly once per key.
func (r *Registry) RegisterEvent(e EventDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[e.Key()] = e
}

// RegisterAction adds an action descriptor with the same overwrite
// rule as RegisterEvent.
func (r *Registry) RegisterAction(a ActionDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions[a.Key()] = a
}

// Event returns the descriptor for key (e.g. "slack.message"), or
// (zero, false) if not registered.
func (r *Registry) Event(key string) (EventDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.events[key]
	return e, ok
}

// Action returns the descriptor for key (e.g. "slack.send_message"),
// or (zero, false) if not registered.
func (r *Registry) Action(key string) (ActionDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[key]
	return a, ok
}

// Events returns a snapshot of every registered event, sorted by key.
func (r *Registry) Events() []EventDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EventDescriptor, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Actions returns a snapshot of every registered action, sorted by key.
func (r *Registry) Actions() []ActionDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ActionDescriptor, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// EventsByChannel returns every event registered under channel, sorted
// by event name. Used by the palette to group rows under each channel.
func (r *Registry) EventsByChannel(channel string) []EventDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []EventDescriptor{}
	for _, e := range r.events {
		if e.Channel == channel {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Event < out[j].Event })
	return out
}

// ActionsByChannel returns every action registered under channel,
// sorted by action name.
func (r *Registry) ActionsByChannel(channel string) []ActionDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []ActionDescriptor{}
	for _, a := range r.actions {
		if a.Channel == channel {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out
}

// Channels returns the de-duplicated list of channel names across both
// events and actions, sorted alphabetically.
func (r *Registry) Channels() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]struct{}{}
	for _, e := range r.events {
		seen[e.Channel] = struct{}{}
	}
	for _, a := range r.actions {
		seen[a.Channel] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
