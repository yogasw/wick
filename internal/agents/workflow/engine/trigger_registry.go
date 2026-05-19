package engine

import (
	"sort"
	"sync"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// TriggerDescriptor bundles the schema + docs for a trigger type
// (cron / channel / webhook / manual / schedule_at / error). One
// descriptor per workflow.TriggerType — the MCP `workflow_node_detail`
// op resolves a `trigger:<type>` key to this struct and projects it
// to the unified detail response.
//
// Schema is a JSON-Schema-ish map of the fields a trigger entry
// carries on workflow.Trigger; Example is a copy-pasteable YAML block.
// Docs is the opt-in self-documentation bundle (examples, quirks,
// templateable fields, pair-with, common pitfalls).
//
// Unlike NodeDescriptor (auto-populated by Engine.Register from the
// executor's Describer method), TriggerDescriptor is hand-registered
// at setup time because triggers don't have executors — they're
// dispatch keys handled by the router. The default catalog lives in
// engine.DefaultTriggerDescriptors() so callers can choose between
// registering the canonical set or overriding individual entries.
type TriggerDescriptor struct {
	Type        workflow.TriggerType
	Description string
	Schema      map[string]any
	Example     string
	wickdocs.Docs
}

// TriggerRegistry holds every TriggerDescriptor wired into one engine.
// Concurrent-safe — registration happens at boot, reads happen for
// every MCP discovery call.
type TriggerRegistry struct {
	mu    sync.RWMutex
	items map[workflow.TriggerType]TriggerDescriptor
}

// NewTriggerRegistry constructs an empty registry. Callers usually
// follow with RegisterMany(DefaultTriggerDescriptors()...) to seed
// the canonical set, then override specific entries to attach Docs.
func NewTriggerRegistry() *TriggerRegistry {
	return &TriggerRegistry{items: map[workflow.TriggerType]TriggerDescriptor{}}
}

// Register adds (or replaces) one descriptor. Replacing is allowed
// so per-channel setup code can attach Docs to a default entry
// without rebuilding the entire catalog.
func (r *TriggerRegistry) Register(d TriggerDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[d.Type] = d
}

// RegisterMany is a convenience wrapper for seeding the registry from
// a slice (typical at boot).
func (r *TriggerRegistry) RegisterMany(ds ...TriggerDescriptor) {
	for _, d := range ds {
		r.Register(d)
	}
}

// Get returns the descriptor for a type, or (zero, false) when not
// registered. Used by `workflow_node_detail` to resolve `trigger:<t>`.
func (r *TriggerRegistry) Get(t workflow.TriggerType) (TriggerDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.items[t]
	return d, ok
}

// List returns a snapshot of every registered descriptor, sorted by
// type so MCP responses are deterministic.
func (r *TriggerRegistry) List() []TriggerDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TriggerDescriptor, 0, len(r.items))
	for _, d := range r.items {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i].Type) < string(out[j].Type) })
	return out
}

// DefaultTriggerDescriptors returns the canonical set of trigger type
// descriptors wick ships with. Callers seed the registry at boot via
// `reg.RegisterMany(engine.DefaultTriggerDescriptors()...)`, then
// optionally override individual entries to attach `Docs` (e.g. the
// channel package decorates `trigger:channel` with the per-trigger
// entry-node multi-trigger quirk).
func DefaultTriggerDescriptors() []TriggerDescriptor {
	return []TriggerDescriptor{
		{
			Type:        workflow.TriggerCron,
			Description: "Run on a cron schedule.",
			Example:     `{type: cron, schedule: "0 8 * * *", timezone: UTC}`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"schedule": map[string]any{"type": "string", "description": "5- or 6-field cron expression."},
					"timezone": map[string]any{"type": "string", "description": "IANA timezone, default UTC."},
				},
				"required": []string{"schedule"},
			},
		},
		{
			Type:        workflow.TriggerChannel,
			Description: "Inbound channel event (message, action, submission, ...).",
			Example:     `{type: channel, channel: slack, event: message, target: "#inbox", entry_node: classify}`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel":       map[string]any{"type": "string", "description": "Channel name (slack, telegram, ...)."},
					"event":         map[string]any{"type": "string", "description": "Event key (message, block_action, ...)."},
					"target":        map[string]any{"type": "string", "description": "Optional channel/user/group target."},
					"entry_node":    map[string]any{"type": "string", "description": "Node id the trigger dispatches to."},
					"match_enabled": map[string]any{"type": "boolean", "description": "Must be true for Match filter to apply."},
					"match":         map[string]any{"type": "object", "description": "Per-event MatchSchema values."},
				},
				"required": []string{"channel", "event", "entry_node"},
			},
			Docs: wickdocs.Docs{
				OutputShape: map[string]string{
					"payload.<event-specific>": "Event payload normalised to flat keys. Slack message: text, user, channel_id, thread, ts, is_dm. Slack block_action: trigger_id, action_id, value, user, channel_id, ts. See workflow_node_detail(channel:<channel>.<event>) for the full per-event payload schema.",
					"type":                     "The TriggerType literal (\"channel\"). Useful when one entry_node serves multiple trigger kinds and you need to branch on origin.",
				},
				Quirks: []string{
					"Each channel trigger on a workflow has its OWN entry_node. Two triggers can route into the same node — the router dispatches per matched trigger, so the entry_node sees one event at a time.",
					"match_enabled is the on/off switch for the per-event MatchSchema. Setting match: {...} without match_enabled: true is a silent no-op — the router ignores the filter.",
					"Picker-typed match values (channel_id, user) must be lists of {id, name} objects. Plain string arrays (e.g. [\"C123\"]) are rejected by the router as malformed.",
					"target is an optional cosmetic hint surfaced in the editor for human readability (e.g. \"#inbox\"). The router does NOT use target for matching — only Match values do.",
				},
				PairWith: []string{
					"trigger:webhook",
					"trigger:cron",
					"trigger:manual",
				},
				CommonPitfalls: []string{
					"Don't expect a single entry_node when you have multiple channel triggers — each trigger declares its own entry_node and the router uses whichever fires.",
					"Don't set match: {...} and forget match_enabled: true — the filter won't apply and the workflow will fire on every event.",
					"Don't write channel_id: [\"C123\"] — the router needs the {id, name} object form for picker fields.",
				},
				InputSample:  `{"type":"channel","channel":"slack","event":"message","entry_node":"classify","match_enabled":true,"match":{"mode":"whitelist","channel_id":[{"id":"C12345","name":"#support"}]}}`,
				OutputSample: `{"type":"channel","channel":"slack","event":"message","at":"2026-05-19T10:32:17Z","payload":{"user":"U02ABCDEF","text":"hi @bot can you check the staging deploy?","channel_id":"C12345","ts":"1700001234.005600","is_dm":false}}`,
				Examples: []wickdocs.Example{
					{
						Name: "single_trigger_no_filter",
						YAML: `triggers:
  - type: channel
    channel: slack
    event: message
    entry_node: classify`,
					},
					{
						Name: "multi_trigger_per_event_routing",
						YAML: `triggers:
  - type: channel
    channel: slack
    event: message
    entry_node: handle_message
  - type: channel
    channel: slack
    event: block_action
    entry_node: handle_action`,
					},
					{
						Name: "with_match_filter",
						YAML: `triggers:
  - type: channel
    channel: slack
    event: message
    entry_node: bug_triage
    match_enabled: true
    match:
      mode: whitelist
      channel_id:
        - { id: C12345, name: "#bugs" }`,
					},
				},
			},
		},
		{
			Type:        workflow.TriggerWebhook,
			Description: "External HTTP POST to /hooks/<path>. HMAC SHA-256 verifiable.",
			Example:     `{type: webhook, path: /hooks/orders/{id}, secret_ref: wick_enc_...}`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string", "description": "URL path under /hooks. Supports {var} segments."},
					"secret_ref": map[string]any{"type": "string", "description": "Encrypted token (wick_enc_...) used for HMAC verification."},
					"entry_node": map[string]any{"type": "string"},
				},
				"required": []string{"path", "entry_node"},
			},
		},
		{
			Type:        workflow.TriggerManual,
			Description: "Admin UI button or MCP workflow_run_now.",
			Example:     `{type: manual, label: "Run digest now", entry_node: start}`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"label":      map[string]any{"type": "string", "description": "Button caption in the editor toolbar."},
					"entry_node": map[string]any{"type": "string"},
				},
				"required": []string{"entry_node"},
			},
		},
		{
			Type:        workflow.TriggerScheduleAt,
			Description: "One-shot fire at a future timestamp.",
			Example:     `{type: schedule_at, at: 2026-06-01T08:00:00Z, entry_node: send}`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"at":         map[string]any{"type": "string", "description": "RFC3339 timestamp."},
					"entry_node": map[string]any{"type": "string"},
				},
				"required": []string{"at", "entry_node"},
			},
		},
		{
			Type:        workflow.TriggerError,
			Description: "Fire on failure of another workflow. Filters by source_workflow / severity / node_types.",
			Example:     `{type: error, source_workflow: "*", severity: [high, critical], entry_node: triage}`,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source_workflow": map[string]any{"type": "string", "description": "Workflow id pattern, * = any."},
					"severity":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"node_types":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"entry_node":      map[string]any{"type": "string"},
				},
				"required": []string{"entry_node"},
			},
		},
	}
}
