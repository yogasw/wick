package handlers

import (
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// TSCall renders one op as the line a wick_run_code script would write:
//
//	await wick.call("conn:…/op", { query: string; limit?: number; mode?: "a" | "b" })
//
// Fields keep the op's declared order; optional ones get "?". Types follow
// ConfigsToJSONSchema (the same params wick_execute takes).
func TSCall(toolID string, in []entity.Config) string {
	schema := ConfigsToJSONSchema(in)
	required := map[string]bool{}
	for _, k := range schema.Required {
		required[k] = true
	}
	var fields []string
	for _, c := range in {
		p, ok := schema.Properties[c.Key]
		if !ok {
			continue
		}
		name := c.Key
		if !isIdent(name) {
			name = strconv.Quote(name)
		}
		if !required[c.Key] {
			name += "?"
		}
		fields = append(fields, name+": "+tsType(p))
	}
	params := "{}"
	if len(fields) > 0 {
		params = "{ " + strings.Join(fields, "; ") + " }"
	}
	return "await wick.call(" + strconv.Quote(toolID) + ", " + params + ")"
}

func tsType(p JSONSchemaProperty) string {
	if len(p.Enum) > 0 {
		vs := make([]string, len(p.Enum))
		for i, e := range p.Enum {
			vs[i] = strconv.Quote(e)
		}
		return strings.Join(vs, " | ")
	}
	switch p.Type {
	case "boolean":
		return "boolean"
	case "number", "integer":
		return "number"
	}
	return "string"
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
