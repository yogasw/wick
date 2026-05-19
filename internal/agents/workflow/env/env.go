// Package env validates and resolves the workflow's env schema +
// values. The Env namespace ({{.Env.X}}) carries non-secret config;
// the Secret namespace ({{.Secret.X}}) decrypts wick_enc_ tokens at
// run time. Plain helpers + leak guard, no filesystem.
package env

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// Widget values mirror config-tags vocab.
const (
	WidgetText     = "text"
	WidgetTextarea = "textarea"
	WidgetSecret   = "secret"
	WidgetNumber   = "number"
	WidgetCheckbox = "checkbox"
	WidgetDropdown = "dropdown"
	WidgetEmail    = "email"
	WidgetURL      = "url"
	WidgetColor    = "color"
	WidgetDate     = "date"
	WidgetDateTime = "datetime"
	WidgetKVList   = "kvlist"
	WidgetPicker   = "picker"
)

// SecretDecryptor unwraps a wick_enc_ token. Engine wires this from
// the existing encrypted-fields service; pass a NoopDecryptor for tests.
type SecretDecryptor interface {
	Decrypt(token string) (string, error)
}

// NoopDecryptor returns tokens unchanged.
type NoopDecryptor struct{}

// Decrypt is a passthrough.
func (NoopDecryptor) Decrypt(token string) (string, error) { return token, nil }

// ValidateValues checks values against schema. Returns an aggregate
// error describing every missing/required/type problem so the UI can
// render all at once.
func ValidateValues(schema []workflow.EnvField, values map[string]string) error {
	var problems []string
	for _, f := range schema {
		if f.Required {
			v, ok := values[f.Name]
			if !ok || strings.TrimSpace(v) == "" {
				problems = append(problems, fmt.Sprintf("missing required env %q", f.Name))
				continue
			}
		}
		if v, ok := values[f.Name]; ok {
			if err := checkWidgetType(f, v); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("env validation: %s", strings.Join(problems, "; "))
}

// OrphanKeys returns values entries not declared in schema.
func OrphanKeys(schema []workflow.EnvField, values map[string]string) []string {
	declared := map[string]bool{}
	for _, f := range schema {
		declared[f.Name] = true
	}
	out := []string{}
	for k := range values {
		if !declared[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func checkWidgetType(f workflow.EnvField, v string) error {
	switch f.Widget {
	case WidgetEmail:
		if !strings.Contains(v, "@") {
			return fmt.Errorf("env %q: not an email", f.Name)
		}
	case WidgetNumber:
		if !looksNumeric(v) {
			return fmt.Errorf("env %q: not a number", f.Name)
		}
	case WidgetCheckbox:
		if v != "true" && v != "false" && v != "1" && v != "0" && v != "" {
			return fmt.Errorf("env %q: checkbox expects bool", f.Name)
		}
	case WidgetDropdown:
		if len(f.Options) == 0 {
			return nil
		}
		for _, opt := range f.Options {
			if opt.ID == v || opt.Name == v {
				return nil
			}
		}
		return fmt.Errorf("env %q: %q not in options", f.Name, v)
	}
	return nil
}

func looksNumeric(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r == '-' && i == 0:
		case r == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return true
}

// ResolveSecrets splits the flat env values map into a plain Env map
// and a Secret map.
func ResolveSecrets(schema []workflow.EnvField, values map[string]string, dec SecretDecryptor) (envMap, secrets map[string]string, err error) {
	envMap = map[string]string{}
	secrets = map[string]string{}
	for _, f := range schema {
		v, ok := values[f.Name]
		if !ok || v == "" {
			if f.Default != "" {
				envMap[f.Name] = f.Default
			}
			continue
		}
		if f.IsSecret() {
			plain := v
			if dec != nil && strings.HasPrefix(v, "wick_enc_") {
				dv, derr := dec.Decrypt(v)
				if derr != nil {
					return nil, nil, fmt.Errorf("decrypt %q: %w", f.Name, derr)
				}
				plain = dv
			}
			secrets[f.Name] = plain
			continue
		}
		envMap[f.Name] = v
	}
	for k, v := range values {
		if _, declared := byName(schema, k); !declared {
			envMap[k] = v
		}
	}
	return envMap, secrets, nil
}

// LeakGuard renders a template and reports if any rendered segment
// contains a known secret value verbatim.
func LeakGuard(rendered string, secrets map[string]string) error {
	for k, v := range secrets {
		if v == "" {
			continue
		}
		if strings.Contains(rendered, v) {
			return fmt.Errorf("rendered output contains secret %q value verbatim — use {{.Secret.%s}} instead", k, k)
		}
	}
	return nil
}

func byName(schema []workflow.EnvField, name string) (workflow.EnvField, bool) {
	for _, f := range schema {
		if f.Name == name {
			return f, true
		}
	}
	return workflow.EnvField{}, false
}

// envFileShape mirrors env.yaml file content for the {values: {...}} variant.
type envFileShape struct {
	Values map[string]string `yaml:"values"`
}

// UnmarshalYAMLFile decodes env.yaml bytes into a plain string→string map.
// Accepts both legacy plain-map form and the {values: {...}} envelope.
func UnmarshalYAMLFile(data []byte, out *map[string]string) error {
	if *out == nil {
		*out = map[string]string{}
	}
	var shape envFileShape
	if err := yaml.Unmarshal(data, &shape); err == nil && shape.Values != nil {
		for k, v := range shape.Values {
			(*out)[k] = v
		}
		return nil
	}
	var plain map[string]string
	if err := yaml.Unmarshal(data, &plain); err != nil {
		return fmt.Errorf("env.yaml decode: %w", err)
	}
	for k, v := range plain {
		(*out)[k] = v
	}
	return nil
}

// MarshalYAMLFile serializes values map to env.yaml bytes.
func MarshalYAMLFile(values map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(envFileShape{Values: values}); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}
