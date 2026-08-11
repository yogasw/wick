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
		// Every spelling of a boolean widget, not just "checkbox".
		//
		// widgetFor lets an explicit tag flag win over the Go type, so a Go bool tagged
		// `wick:"bool"` yields the widget "bool" rather than "checkbox" — and this switch
		// only knew "checkbox", so it fell through to the default and declared the field a
		// STRING. Parsing was unaffected (CfgBool accepts "true"/"false"), but a caller
		// reading the schema had no way to know whether to send true, "true" or "1".
		case "checkbox", "bool", "boolean":
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

// splitOptions splits a dropdown's option list.
//
// The separator is "|", per entity.Config: "Options is pipe-separated (a|b|c)".
// This split on "," instead, so a dropdown declared as `wick:"dropdown=push|pop|list"`
// produced a one-element enum holding the literal "push|pop|list" — a schema that
// rejects every value that is actually valid. wick does not validate enums, so it went
// unnoticed here, but a strict JSON Schema validator on the caller's side would refuse
// the only correct inputs.
//
// Comma is still accepted, so an option list written the wrong way keeps working
// rather than collapsing into one bogus value.
func splitOptions(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '|' || r == ',' })
	out := parts[:0]
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
