package wick

import (
	"strings"

	"google.golang.org/genai"
)

// schemaToJSON converts a genai.Schema into a plain JSON-Schema map with
// lowercase type names, the shape OpenAI/Anthropic tool parameter
// schemas expect. genai encodes Type as uppercase ("STRING", "OBJECT");
// vendors want lowercase ("string", "object"). nil → a permissive empty
// object schema so a tool with no params still validates.
func schemaToJSON(s *genai.Schema) map[string]any {
	if s == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := map[string]any{}
	if s.Type != "" {
		out["type"] = strings.ToLower(string(s.Type))
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		out["enum"] = s.Enum
	}
	if s.Format != "" && s.Format != "enum" {
		out["format"] = s.Format
	}
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaToJSON(v)
		}
		out["properties"] = props
	}
	if len(s.Required) > 0 {
		out["required"] = s.Required
	}
	if s.Items != nil {
		out["items"] = schemaToJSON(s.Items)
	}
	return out
}

// toolsFromConfig extracts the flat list of function declarations from a
// gen config's Tools field (the engine puts all wick tools under a
// single genai.Tool.FunctionDeclarations).
func toolsFromConfig(cfg *genai.GenerateContentConfig) []*genai.FunctionDeclaration {
	if cfg == nil {
		return nil
	}
	var out []*genai.FunctionDeclaration
	for _, t := range cfg.Tools {
		if t == nil {
			continue
		}
		out = append(out, t.FunctionDeclarations...)
	}
	return out
}

// systemText extracts the plain system-instruction text from a gen
// config, concatenating any text parts.
func systemText(cfg *genai.GenerateContentConfig) string {
	if cfg == nil || cfg.SystemInstruction == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range cfg.SystemInstruction.Parts {
		if p != nil && p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}
