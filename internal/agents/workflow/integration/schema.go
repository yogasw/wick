package integration

import (
	"reflect"
	"strings"
	"unicode"
)

// StructSchema reflects a typed Input/Output struct into a JSON Schema object
// using `wick` and `json` struct tags. Used by workflow_integration to expose
// per-field schemas for channel action args so AI knows exactly which args
// each action accepts, which are required, and which support expression mode.
//
// Rules:
//   - Only fields with a `wick` tag are included (excludes output-only fields).
//   - Key = json tag name if present, else wick key=, else snake_case of field name.
//   - required → added to "required" array.
//   - desc= → "description".
//   - textarea → "format":"textarea" hint (field supports multiline / Block Kit JSON).
//   - bool → type boolean, int/float → type number, else string.
//
// Returns nil for non-struct types.
func StructSchema(v any) map[string]any {
	if v == nil {
		return nil
	}
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	props := map[string]any{}
	required := []string{}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		raw := f.Tag.Get("wick")
		if raw == "" {
			continue
		}
		tag := parseWickTag(raw)

		key := jsonKey(f)
		if key == "" {
			key = tag["key"]
		}
		if key == "" {
			key = toSnakeCase(f.Name)
		}

		prop := map[string]any{}
		switch f.Type.Kind() {
		case reflect.Bool:
			prop["type"] = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			prop["type"] = "number"
		default:
			prop["type"] = "string"
		}

		if desc := tag["desc"]; desc != "" {
			prop["description"] = desc
		}
		if _, ok := tag["textarea"]; ok {
			prop["format"] = "textarea"
		}

		props[key] = prop

		if tag["required"] == "true" {
			required = append(required, key)
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

func jsonKey(f reflect.StructField) string {
	raw := f.Tag.Get("json")
	if raw == "" || raw == "-" {
		return ""
	}
	if idx := strings.IndexByte(raw, ','); idx >= 0 {
		raw = raw[:idx]
	}
	return raw
}

func parseWickTag(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if eq := strings.IndexByte(part, '='); eq >= 0 {
			out[strings.TrimSpace(part[:eq])] = strings.TrimSpace(part[eq+1:])
		} else {
			out[part] = "true"
		}
	}
	return out
}

func toSnakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next)) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
