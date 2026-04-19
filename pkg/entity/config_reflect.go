package entity

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// StructToConfigs reflects a typed per-module Config struct into a
// slice of Config rows ready for reconciliation. Each exported field
// with a non-empty `wick:"..."` tag becomes one row.
//
// Field name → Key: CamelCase → snake_case (InitText → init_text),
// unless overridden by `key=...` in the tag.
//
// Field type → widget:
//
//	bool   → "checkbox"
//	int*   → "number"
//	string → "text" (or whatever tag flag says: textarea, dropdown,
//	                 email, url, color, date, datetime)
//
// Field value → seed Value. Zero values are written as empty string
// for string fields, "0" for int fields, "false" for bool fields —
// reconcile then either inserts the row or preserves the existing DB
// value.
//
// Fields without a `wick` tag are ignored, so Config structs can hold
// internal state that should not become a row.
func StructToConfigs(cfg any) []Config {
	v := reflect.ValueOf(cfg)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()

	var out []Config
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

		key := tag["key"]
		if key == "" {
			key = toSnakeCase(f.Name)
		}

		widget, opts := widgetFor(f.Type.Kind(), tag)

		out = append(out, Config{
			Key:           key,
			Value:         goValueToString(v.Field(i)),
			Type:          widget,
			Options:       opts,
			IsSecret:      tag["secret"] == "true",
			CanRegenerate: tag["regen"] == "true",
			Locked:        tag["locked"] == "true",
			Required:      tag["required"] == "true",
			Description:   tag["desc"],
		})
	}
	return out
}

// parseWickTag splits the `wick:"..."` tag payload into a map. Entries
// are separated by `;`; key=value pairs use `=`; bare keys become
// "true" (flag form).
//
// Example: "desc=API key.;secret;required" →
//
//	{"desc": "API key.", "secret": "true", "required": "true"}
func parseWickTag(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			out[part] = "true"
			continue
		}
		out[strings.TrimSpace(part[:eq])] = strings.TrimSpace(part[eq+1:])
	}
	return out
}

// widgetFor picks the admin UI widget from the Go type + tag flags.
// Explicit widget flags in the tag win over the type-derived default.
func widgetFor(k reflect.Kind, tag map[string]string) (widget, options string) {
	for _, flag := range []string{"textarea", "dropdown", "email", "url", "color", "date", "datetime", "number", "checkbox"} {
		if v, ok := tag[flag]; ok {
			if flag == "dropdown" {
				return "dropdown", v
			}
			return flag, ""
		}
	}
	switch k {
	case reflect.Bool:
		return "checkbox", ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number", ""
	default:
		return "text", ""
	}
}

// goValueToString renders a reflect.Value as the string form stored in
// Config.Value. Booleans become "true"/"false", numbers use %v,
// strings are verbatim.
func goValueToString(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%g", v.Float())
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

// toSnakeCase converts CamelCase/PascalCase to snake_case. Runs of
// uppercase letters stay together until the final boundary
// (APIBaseURL → api_base_url).
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
