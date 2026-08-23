package wick

import (
	"context"
	"strings"

	"google.golang.org/genai"
)

// external_tools.go is the seam through which the FULL wick MCP tool
// surface is exposed to the in-process agent — not just connectors, but
// ask_user, wick_set_title, wick_session_info, wick_session_workspace,
// wick_list/get/execute, wick_list_providers, wick_skill_*, schedule,
// wickmanager, … — everything the MCP server dispatches.
//
// The wick package can't import internal/mcp (layering + cycle risk), so
// the server wires a provider here at boot. The provider reuses the real
// MCP handlers in-process, running as the human who owns the session
// (ToolScope.UserID) so tool visibility matches what that user may reach —
// the same answer the HTTP MCP path gives. An ownerless session falls back
// to the synthetic admin principal (internal/mcp/auth.go internalSystemUser).

// ExternalTool is one tool contributed by the host (the MCP surface).
// Params is a JSON-Schema object (as the MCP tool descriptor carries it);
// wick converts it to the genai schema the model adapters need.
type ExternalTool struct {
	Name        string
	Description string
	Params      map[string]any // JSON Schema object
	Handler     func(ctx context.Context, args map[string]any) (result string, isError bool)
}

// ToolScope identifies the spawn a tool set is built for, so session-aware
// tools (ask_user, wick_session_*) resolve the right session.
type ToolScope struct {
	SessionID string
	Workspace string
}

// externalToolProvider is set by the server (SetToolProvider). nil = only
// the built-in shell/todo tools are available.
var externalToolProvider func(ToolScope) []ExternalTool

// SetToolProvider wires the host tool provider (the MCP surface). Called
// once at server boot.
func SetToolProvider(fn func(ToolScope) []ExternalTool) { externalToolProvider = fn }

// externalToolDefs asks the provider for the tools available to this
// spawn and converts them to the engine's toolDef shape. Returns nil when
// no provider is wired.
func externalToolDefs(scope ToolScope) []toolDef {
	if externalToolProvider == nil {
		return nil
	}
	ext := externalToolProvider(scope)
	out := make([]toolDef, 0, len(ext))
	for _, e := range ext {
		e := e
		out = append(out, toolDef{
			decl: &genai.FunctionDeclaration{
				Name:        e.Name,
				Description: e.Description,
				Parameters:  jsonSchemaToGenai(e.Params),
			},
			handler: e.Handler,
		})
	}
	return out
}

// jsonSchemaToGenai converts a JSON-Schema object (map form, as MCP tool
// descriptors carry it) into a *genai.Schema — the inverse of
// schemaToJSON. Unknown / missing type defaults to OBJECT so a tool with
// an odd schema still registers.
func jsonSchemaToGenai(m map[string]any) *genai.Schema {
	if len(m) == 0 {
		return &genai.Schema{Type: genai.TypeObject}
	}
	s := &genai.Schema{}
	if t, ok := m["type"].(string); ok {
		s.Type = genaiType(t)
	}
	if d, ok := m["description"].(string); ok {
		s.Description = d
	}
	if f, ok := m["format"].(string); ok {
		s.Format = f
	}
	if enum, ok := m["enum"].([]any); ok {
		for _, e := range enum {
			if es, ok := e.(string); ok {
				s.Enum = append(s.Enum, es)
			}
		}
	}
	if req, ok := m["required"].([]any); ok {
		for _, r := range req {
			if rs, ok := r.(string); ok {
				s.Required = append(s.Required, rs)
			}
		}
	}
	if props, ok := m["properties"].(map[string]any); ok && len(props) > 0 {
		s.Properties = make(map[string]*genai.Schema, len(props))
		for k, v := range props {
			if pm, ok := v.(map[string]any); ok {
				s.Properties[k] = jsonSchemaToGenai(pm)
			}
		}
	}
	if items, ok := m["items"].(map[string]any); ok {
		s.Items = jsonSchemaToGenai(items)
	}
	if s.Type == "" {
		s.Type = genai.TypeObject
	}
	return s
}

// genaiType maps a lowercase JSON-Schema type to the genai uppercase enum.
func genaiType(t string) genai.Type {
	switch strings.ToLower(t) {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeObject
	}
}
