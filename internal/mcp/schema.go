package mcp

import (
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// jsonSchema is the minimal JSON Schema (draft-07-ish) shape the MCP
// spec wants for tools/list inputSchema. Only the fields LLM clients
// actually read are included — extra noise tends to confuse parsers.
type jsonSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]jsonSchemaProperty `json:"properties,omitempty"`
	Required   []string                     `json:"required,omitempty"`
}

type jsonSchemaProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Format      string   `json:"format,omitempty"`
}

// configsToJSONSchema turns the per-op Input rows reflected from a
// typed Go struct into a JSON Schema MCP clients can present to the
// LLM. Empty inputs collapse to an empty-object schema (still valid).
func configsToJSONSchema(cfgs []entity.Config) jsonSchema {
	out := jsonSchema{
		Type:       "object",
		Properties: make(map[string]jsonSchemaProperty, len(cfgs)),
	}
	for _, c := range cfgs {
		prop := jsonSchemaProperty{
			Description: c.Description,
		}
		switch c.Type {
		case "checkbox":
			prop.Type = "boolean"
		case "number":
			prop.Type = "number"
		case "dropdown":
			prop.Type = "string"
			if c.Options != "" {
				prop.Enum = splitOptions(c.Options)
			}
		case "email":
			prop.Type = "string"
			prop.Format = "email"
		case "url":
			prop.Type = "string"
			prop.Format = "uri"
		case "date":
			prop.Type = "string"
			prop.Format = "date"
		case "datetime":
			prop.Type = "string"
			prop.Format = "date-time"
		default:
			prop.Type = "string"
		}
		out.Properties[c.Key] = prop
		if c.Required {
			out.Required = append(out.Required, c.Key)
		}
	}
	return out
}

// splitOptions parses a dropdown options string. Format follows the
// `wick:"dropdown=foo,bar,baz"` convention used by entity.Config —
// comma-separated values with whitespace tolerance.
func splitOptions(raw string) []string {
	parts := strings.Split(raw, ",")
	out := parts[:0]
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

