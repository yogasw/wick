// Package channel is a thin adapter over internal/agents/channels.
// Workflow code uses local types (Channel, TriggerSpec, ActionSpec,
// Registry) so node executors / MCP / inject stay simple, but the
// authoritative declarations live on each transport in
// internal/agents/channels/<name>/. Channels opt in by implementing
// agentchannels.WorkflowTriggerProvider + WorkflowActionProvider +
// (optionally) WorkflowSessionOriginator.
package channel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// Channel is the workflow-facing surface — a thin alias-style view onto
// the underlying agentchannels.Channel plus its workflow opt-ins. Local
// type so existing call sites compile unchanged.
type Channel interface {
	Name() string
	TriggerSpecs() []TriggerSpec
	Actions() []ActionSpec
	Send(ctx context.Context, op string, args map[string]any) (any, error)
	SupportsSession() bool
}

// TriggerSpec mirrors agentchannels.WorkflowTriggerSpec. Kept local so
// the workflow package never imports agentchannels for value types in
// generated JSON / MCP payloads.
type TriggerSpec struct {
	Type          string         `json:"type"`
	Events        []string       `json:"events"`
	Description   string         `json:"description"`
	MatchSchema   map[string]any `json:"match_schema,omitempty"`
	PayloadSchema map[string]any `json:"payload_schema,omitempty"`
}

// ActionSpec mirrors agentchannels.WorkflowActionSpec.
type ActionSpec struct {
	ID           string         `json:"id"`
	Description  string         `json:"description"`
	Destructive  bool           `json:"destructive,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

// Registry is the workflow view of registered channels. It wraps an
// agentchannels.Registry — channels are registered ONCE in the base
// registry; the workflow registry just filters those that opt into the
// workflow surface.
type Registry struct {
	mu    sync.RWMutex
	base  *agentchannels.Registry
	extra map[string]Channel // test-only direct registrations
}

// NewRegistry constructs an empty registry (no base wired). Call
// SetBase or use NewRegistryFromBase once the agentchannels.Registry is
// constructed in server.go.
func NewRegistry() *Registry {
	return &Registry{extra: map[string]Channel{}}
}

// NewRegistryFromBase wraps an existing agentchannels.Registry.
func NewRegistryFromBase(base *agentchannels.Registry) *Registry {
	return &Registry{base: base, extra: map[string]Channel{}}
}

// SetBase rewires the underlying base registry. Used by setup composer
// once the server-side channel registry is built.
func (r *Registry) SetBase(base *agentchannels.Registry) {
	r.mu.Lock()
	r.base = base
	r.mu.Unlock()
}

// Register adds a workflow-only test double directly (bypassing the
// base registry). Production code registers channels via
// agentchannels.Registry.Add and never calls this.
func (r *Registry) Register(ch Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extra[ch.Name()] = ch
}

// Get looks up a channel by name. Resolves from the base registry first
// (filtered to those opting into the workflow surface), then from extras.
func (r *Registry) Get(name string) (Channel, bool) {
	r.mu.RLock()
	base := r.base
	if ch, ok := r.extra[name]; ok {
		r.mu.RUnlock()
		return ch, true
	}
	r.mu.RUnlock()
	if base == nil {
		return nil, false
	}
	raw := base.ChannelByName(name)
	if raw == nil {
		return nil, false
	}
	if !optsIntoWorkflow(raw) {
		return nil, false
	}
	return wrap(raw), true
}

// List returns all workflow-visible channel names sorted. One entry per
// channel TYPE — a type registered once per owning user (slack) still
// appears once, because the name is what a node stores.
func (r *Registry) List() []string {
	r.mu.RLock()
	base := r.base
	seen := make(map[string]struct{}, len(r.extra))
	out := make([]string, 0, len(r.extra))
	for n := range r.extra {
		seen[n] = struct{}{}
		out = append(out, n)
	}
	r.mu.RUnlock()
	if base != nil {
		for _, raw := range base.Channels() {
			if !optsIntoWorkflow(raw) {
				continue
			}
			if _, dup := seen[raw.Name()]; dup {
				continue
			}
			seen[raw.Name()] = struct{}{}
			out = append(out, raw.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Describe returns introspection rows for `workflow_channels` MCP op —
// ONE ROW PER REGISTERED INSTANCE, not per channel type. A channel type
// like Slack is registered once per owning user ("slack:<user-id>"), and
// every one of those instances is a different bot in a possibly
// different workspace. Collapsing them to the bare type name is what
// made the editor's Channel picker show five identical "slack" rows with
// no way to tell whose bot each one is.
//
// Name stays the channel type (that is what a node stores and what the
// engine resolves), while InstanceKey / OwnerUserID / BotName carry the
// identity the UI needs to label the row. The HTTP catalog fills Label
// with the owner's display name — the registry has no user store.
func (r *Registry) Describe() []Info {
	r.mu.RLock()
	base := r.base
	extras := make([]Channel, 0, len(r.extra))
	for _, ch := range r.extra {
		extras = append(extras, ch)
	}
	r.mu.RUnlock()

	out := []Info{}
	seen := map[string]struct{}{}
	for _, ch := range extras {
		seen[ch.Name()] = struct{}{}
		out = append(out, Info{
			Name:            ch.Name(),
			Label:           ch.Name(),
			Triggers:        ch.TriggerSpecs(),
			Actions:         ch.Actions(),
			SupportsSession: ch.SupportsSession(),
			Configured:      true,
		})
	}
	if base != nil {
		for _, raw := range base.Channels() {
			if !optsIntoWorkflow(raw) {
				continue
			}
			if _, dup := seen[raw.Name()]; dup {
				continue
			}
			ch := wrap(raw)
			key := base.InstanceKeyOf(raw)
			out = append(out, Info{
				Name:            raw.Name(),
				Label:           raw.Name(),
				InstanceKey:     key,
				OwnerUserID:     OwnerFromInstanceKey(key),
				BotName:         botNameOf(raw),
				Configured:      configuredOf(raw),
				Triggers:        ch.TriggerSpecs(),
				Actions:         ch.Actions(),
				SupportsSession: ch.SupportsSession(),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].InstanceKey < out[j].InstanceKey
	})
	return out
}

// Info is one row of the introspection response — one registered channel
// INSTANCE. Name is the channel type a node stores; the rest identifies
// which bot / whose row that instance is.
type Info struct {
	Name            string        `json:"name"`
	Label           string        `json:"label,omitempty"`
	InstanceKey     string        `json:"instance_key,omitempty"`
	OwnerUserID     string        `json:"owner_user_id,omitempty"`
	OwnerName       string        `json:"owner_name,omitempty"`
	BotName         string        `json:"bot_name,omitempty"`
	Configured      bool          `json:"configured"`
	Triggers        []TriggerSpec `json:"triggers"`
	Actions         []ActionSpec  `json:"actions"`
	SupportsSession bool          `json:"supports_session"`
}

// OwnerFromInstanceKey extracts the wick user id an instance was
// registered for. Keys are "<type>:<user-id>", with the sentinel
// "__owner__" standing for the App Owner row (user_id = NULL), which
// reports "" — the same value the rest of the codebase uses for it.
func OwnerFromInstanceKey(key string) string {
	i := strings.Index(key, ":")
	if i < 0 {
		return ""
	}
	owner := key[i+1:]
	if owner == "__owner__" {
		return ""
	}
	return owner
}

// botNameOf reads the bot handle an instance resolved at connect time
// (Slack's auth.test). Empty until the instance has connected, or for
// transports with no bot identity.
func botNameOf(raw agentchannels.Channel) string {
	named, ok := raw.(agentchannels.BotNamer)
	if !ok {
		return ""
	}
	return named.BotUserName()
}

// configuredOf reports whether an instance holds the credentials it
// needs to run. Channels that don't expose the check count as configured
// so they aren't hidden from the picker.
func configuredOf(raw agentchannels.Channel) bool {
	c, ok := raw.(interface{ IsConfigured() bool })
	if !ok {
		return true
	}
	return c.IsConfigured()
}

// ValidateActionInput checks `args` against a spec's required keys.
func ValidateActionInput(spec ActionSpec, args map[string]any) error {
	props, _ := spec.InputSchema["properties"].(map[string]any)
	if props == nil {
		return nil
	}
	required, _ := spec.InputSchema["required"].([]any)
	for _, r := range required {
		name, _ := r.(string)
		if name == "" {
			continue
		}
		if _, ok := args[name]; !ok {
			return missingArgError{op: spec.ID, name: name}
		}
	}
	return nil
}

type missingArgError struct {
	op   string
	name string
}

func (e missingArgError) Error() string {
	return "missing required arg \"" + e.name + "\" for op \"" + e.op + "\""
}

// ── adapter glue ─────────────────────────────────────────────────────

func optsIntoWorkflow(raw agentchannels.Channel) bool {
	if _, ok := raw.(agentchannels.WorkflowTriggerProvider); ok {
		return true
	}
	if _, ok := raw.(agentchannels.WorkflowActionProvider); ok {
		return true
	}
	return false
}

type adapter struct{ raw agentchannels.Channel }

func wrap(raw agentchannels.Channel) Channel { return adapter{raw: raw} }

func (a adapter) Name() string { return a.raw.Name() }

func (a adapter) TriggerSpecs() []TriggerSpec {
	tp, ok := a.raw.(agentchannels.WorkflowTriggerProvider)
	if !ok {
		return nil
	}
	src := tp.WorkflowTriggerSpecs()
	out := make([]TriggerSpec, len(src))
	for i, s := range src {
		out[i] = TriggerSpec(s)
	}
	return out
}

func (a adapter) Actions() []ActionSpec {
	ap, ok := a.raw.(agentchannels.WorkflowActionProvider)
	if !ok {
		return nil
	}
	src := ap.WorkflowActionSpecs()
	out := make([]ActionSpec, len(src))
	for i, s := range src {
		out[i] = ActionSpec(s)
	}
	return out
}

func (a adapter) Send(ctx context.Context, op string, args map[string]any) (any, error) {
	ap, ok := a.raw.(agentchannels.WorkflowActionProvider)
	if !ok {
		return nil, fmt.Errorf("channel %q has no workflow action surface", a.raw.Name())
	}
	return ap.WorkflowSend(ctx, op, args)
}

func (a adapter) SupportsSession() bool {
	so, ok := a.raw.(agentchannels.WorkflowSessionOriginator)
	if !ok {
		return false
	}
	return so.SupportsSession()
}
