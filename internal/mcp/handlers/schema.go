package handlers

import (
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// JSONSchema is the minimal JSON Schema shape the MCP spec wants for
// tools/list inputSchema.
type JSONSchema struct {
	Type       string                        `json:"type"`
	Properties map[string]JSONSchemaProperty `json:"properties,omitempty"`
	Required   []string                      `json:"required,omitempty"`
}

// JSONSchemaProperty is one property entry inside a JSONSchema.
type JSONSchemaProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Format      string   `json:"format,omitempty"`
}

// ConfigsToJSONSchema turns per-op Input rows into a JSON Schema.
func ConfigsToJSONSchema(cfgs []entity.Config) JSONSchema {
	out := JSONSchema{
		Type:       "object",
		Properties: make(map[string]JSONSchemaProperty, len(cfgs)),
	}
	for _, c := range cfgs {
		prop := JSONSchemaProperty{Description: c.Description}
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
