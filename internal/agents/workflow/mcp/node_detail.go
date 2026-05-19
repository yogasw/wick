// Package mcp — workflow_node_detail implementation.
//
// One MCP op resolves a `node_type` key (built-in node, channel
// event/action, connector op, trigger type) to a unified detail
// response. Source-of-truth descriptors stay where they live
// (engine.NodeDescriptor, integration.EventDescriptor /
// ActionDescriptor, connector.Operation, engine.TriggerDescriptor) —
// this file is the projector that lifts each one into the same
// `NodeDetail` JSON shape so AI clients can fetch detail without
// knowing the per-source struct.
//
// Key format (mirrored in the wick-workflow MCP guide):
//
//	agent                              built-in node
//	channel:slack.message              channel trigger event
//	channel:slack.send_message         channel action
//	connector:slack.chat_postMessage   connector op
//	trigger:cron                       trigger type
//
// All optional fields from wickdocs.Docs are flattened into the
// response; empty fields are omitted via json:",omitempty" so the AI
// never branches on null.
package mcp

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// NodeDetail is the unified `workflow_node_detail` response.
//
// Kind is set per-source so the AI can disambiguate which prefix to
// re-use when chasing PairWith links; it is informational only.
type NodeDetail struct {
	NodeType    string         `json:"node_type"`
	Kind        string         `json:"kind"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	WhenToUse   string         `json:"when_to_use,omitempty"`
	Destructive bool           `json:"destructive,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Output      map[string]any `json:"output,omitempty"`
	MatchSchema map[string]any `json:"match_schema,omitempty"`
	Example     string         `json:"example,omitempty"`
	wickdocs.Docs
}

// Detail kinds, exposed via NodeDetail.Kind.
const (
	KindBuiltIn       = "built_in"
	KindChannelEvent  = "channel_event"
	KindChannelAction = "channel_action"
	KindConnectorOp   = "connector_op"
	KindTrigger       = "trigger"
)

// NodeDetail returns the unified detail for one node_type key, or an
// error when the key is malformed or not registered.
//
// Resolution order is by prefix — `channel:`, `connector:`, `trigger:`
// route to the matching registry; everything else is treated as a
// built-in node type. Unknown keys return an "unknown node_type" error
// so AI clients see a clean signal rather than an empty payload.
func (m *Ops) NodeDetail(nodeType string) (NodeDetail, error) {
	key := strings.TrimSpace(nodeType)
	if key == "" {
		return NodeDetail{}, fmt.Errorf("node_type is required")
	}
	switch {
	case strings.HasPrefix(key, "channel:"):
		return m.channelDetail(strings.TrimPrefix(key, "channel:"))
	case strings.HasPrefix(key, "connector:"):
		return m.connectorDetail(strings.TrimPrefix(key, "connector:"))
	case strings.HasPrefix(key, "trigger:"):
		return m.triggerDetail(strings.TrimPrefix(key, "trigger:"))
	default:
		return m.builtInDetail(key)
	}
}

// builtInDetail resolves a built-in node type (agent, branch, http, ...)
// against Engine.Descriptors.
func (m *Ops) builtInDetail(t string) (NodeDetail, error) {
	if m.Engine == nil {
		return NodeDetail{}, fmt.Errorf("engine not configured")
	}
	desc, ok := m.Engine.Descriptors[workflow.NodeType(t)]
	if !ok {
		return NodeDetail{}, fmt.Errorf("unknown node_type %q", t)
	}
	out := outputMap(desc.Output)
	return NodeDetail{
		NodeType:    t,
		Kind:        KindBuiltIn,
		Description: desc.Description,
		WhenToUse:   desc.WhenToUse,
		Schema:      desc.Schema,
		Output:      out,
		Example:     desc.Example,
		Docs:        desc.Docs,
	}, nil
}

// channelDetail resolves a `channel:<channel>.<event|action>` key. The
// suffix after the dot is matched first against events, then actions —
// channel/event and channel/action namespaces never collide because
// EventDescriptor.Key() uses ".<event>" and ActionDescriptor.Key()
// uses ".<action>" with distinct event/action name lists per channel.
func (m *Ops) channelDetail(rest string) (NodeDetail, error) {
	if m.Integration == nil {
		return NodeDetail{}, fmt.Errorf("integration registry not configured")
	}
	if ev, ok := m.Integration.Event(rest); ok {
		return NodeDetail{
			NodeType:    "channel:" + rest,
			Kind:        KindChannelEvent,
			Name:        ev.Name,
			Description: ev.Description,
			Schema:      integration.StructSchema(ev.PayloadType),
			MatchSchema: configsToSchema(ev.MatchSchema),
			Docs:        ev.Docs,
		}, nil
	}
	if act, ok := m.Integration.Action(rest); ok {
		return NodeDetail{
			NodeType:    "channel:" + rest,
			Kind:        KindChannelAction,
			Name:        act.Name,
			Description: act.Description,
			Destructive: act.Destructive,
			Schema:      integration.StructSchema(act.InputType),
			Output:      stringSchemaMap(integration.StructSchema(act.OutputType)),
			Docs:        act.Docs,
		}, nil
	}
	return NodeDetail{}, fmt.Errorf("unknown channel node %q", rest)
}

// connectorDetail resolves a `connector:<module>.<op>` key. The
// connector registry stores ops on the Module — we look them up by
// linear scan since op count per module stays small (single-digit to
// low-double-digit).
func (m *Ops) connectorDetail(rest string) (NodeDetail, error) {
	if m.Connectors == nil {
		return NodeDetail{}, fmt.Errorf("connector registry not configured")
	}
	dot := strings.IndexByte(rest, '.')
	if dot < 0 {
		return NodeDetail{}, fmt.Errorf("connector key must be <module>.<op>, got %q", rest)
	}
	moduleKey, opKey := rest[:dot], rest[dot+1:]
	mod, ok := m.Connectors.Module(moduleKey)
	if !ok {
		return NodeDetail{}, fmt.Errorf("unknown connector module %q", moduleKey)
	}
	for _, op := range mod.Operations {
		if op.Key != opKey {
			continue
		}
		return NodeDetail{
			NodeType:    "connector:" + rest,
			Kind:        KindConnectorOp,
			Name:        op.Name,
			Description: op.Description,
			Destructive: op.Destructive,
			Schema:      pkgConfigsToSchema(op.Input),
			Docs:        op.Docs,
		}, nil
	}
	return NodeDetail{}, fmt.Errorf("unknown connector op %q on module %q", opKey, moduleKey)
}

// triggerDetail resolves a `trigger:<type>` key.
func (m *Ops) triggerDetail(t string) (NodeDetail, error) {
	if m.Engine == nil || m.Engine.Triggers == nil {
		return NodeDetail{}, fmt.Errorf("trigger registry not configured")
	}
	desc, ok := m.Engine.Triggers.Get(workflow.TriggerType(t))
	if !ok {
		return NodeDetail{}, fmt.Errorf("unknown trigger %q", t)
	}
	return NodeDetail{
		NodeType:    "trigger:" + t,
		Kind:        KindTrigger,
		Description: desc.Description,
		Schema:      desc.Schema,
		Example:     desc.Example,
		Docs:        desc.Docs,
	}, nil
}

// outputMap turns NodeDescriptor.Output (map[string]string) into the
// loose any-shape NodeDetail.Output uses so we can also carry richer
// reflected schemas from channel actions without changing the
// response shape.
func outputMap(out map[string]string) map[string]any {
	if len(out) == 0 {
		return nil
	}
	r := make(map[string]any, len(out))
	for k, v := range out {
		r[k] = v
	}
	return r
}

// stringSchemaMap downcasts a JSON-Schema-shaped map[string]any to the
// NodeDetail.Output field. We pass it through unchanged so the AI
// gets the full reflected struct.
func stringSchemaMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	return in
}

// configsToSchema reflects an internal/entity.Config slice (used for
// MatchSchema on events) into a JSON-Schema-ish map so AI sees fields
// + types + descriptions in the same shape as NodeDetail.Schema.
func configsToSchema(cfgs []entity.Config) map[string]any {
	if len(cfgs) == 0 {
		return nil
	}
	props := map[string]any{}
	required := []string{}
	for _, c := range cfgs {
		prop := configToProp(c)
		props[c.Key] = prop
		if c.Required {
			required = append(required, c.Key)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// pkgConfigsToSchema is the connector-side variant. Connector ops
// store Input as []pkg/entity.Config (the public type) rather than
// the internal alias used for channel MatchSchemas, so we duplicate
// the conversion against the pkg shape rather than reaching into the
// internal alias.
func pkgConfigsToSchema(in []pkgentity.Config) map[string]any {
	if len(in) == 0 {
		return nil
	}
	props := map[string]any{}
	required := []string{}
	for _, c := range in {
		prop := map[string]any{"type": configType(c.Type)}
		if c.Description != "" {
			prop["description"] = c.Description
		}
		if c.Type == "dropdown" && c.Options != "" {
			prop["enum"] = strings.Split(c.Options, "|")
		}
		props[c.Key] = prop
		if c.Required {
			required = append(required, c.Key)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// configToProp builds a single property entry from one entity.Config.
// Mirrors integration.StructSchema's mapping but works off the
// already-reflected Config rows (the event MatchSchema is stored as
// []Config, not a struct).
func configToProp(c entity.Config) map[string]any {
	prop := map[string]any{"type": configType(c.Type)}
	if c.Description != "" {
		prop["description"] = c.Description
	}
	if c.Type == "dropdown" && c.Options != "" {
		prop["enum"] = strings.Split(c.Options, "|")
	}
	if c.VisibleWhen != "" {
		prop["visible_when"] = c.VisibleWhen
	}
	return prop
}

// configType maps the entity.Config.Type widget name to a JSON Schema
// primitive. Unknown widgets default to "string" since the runtime
// stores every config value as a string.
func configType(t string) string {
	switch t {
	case "checkbox":
		return "boolean"
	case "number":
		return "number"
	default:
		return "string"
	}
}

