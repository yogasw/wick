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

// List returns all workflow-visible channel names sorted.
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
			out = append(out, raw.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Describe returns introspection rows for `workflow_channels` MCP op.
// Only includes channels that opt into the workflow surface.
func (r *Registry) Describe() []Info {
	out := []Info{}
	for _, name := range r.List() {
		ch, ok := r.Get(name)
		if !ok {
			continue
		}
		out = append(out, Info{
			Name:            name,
			Triggers:        ch.TriggerSpecs(),
			Actions:         ch.Actions(),
			SupportsSession: ch.SupportsSession(),
		})
	}
	return out
}

// Info is one row of the introspection response.
type Info struct {
	Name            string        `json:"name"`
	Triggers        []TriggerSpec `json:"triggers"`
	Actions         []ActionSpec  `json:"actions"`
	SupportsSession bool          `json:"supports_session"`
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
