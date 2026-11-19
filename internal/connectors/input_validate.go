package connectors

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// InputError reports params an agent sent that do not fit the operation's
// declared inputs. It is returned by Execute BEFORE the operation runs, so
// a wrong guess never reaches the upstream service. The MCP layer turns it
// into a reply that carries the op's full input schema, which lets an agent
// call wick_execute straight from a known signature and fix a miss in one
// retry instead of always fetching the schema first.
type InputError struct {
	Operation string
	// Problems lists each violation in plain words (missing required field,
	// value outside the enum, not a number, not a boolean).
	Problems []string
	// Unknown lists param keys the op does not declare. They never fail a
	// call on their own (some connectors read undeclared keys), but they
	// are reported next to real problems because a misspelled key is the
	// usual cause of a "missing required" one.
	Unknown []string
	// Inputs is the op's declared input list, for rendering its schema.
	Inputs []entity.Config
}

func (e *InputError) Error() string {
	msg := fmt.Sprintf("invalid params for %q: %s", e.Operation, strings.Join(e.Problems, "; "))
	if len(e.Unknown) > 0 {
		msg += fmt.Sprintf(" (unknown params: %s)", strings.Join(e.Unknown, ", "))
	}
	return msg
}

// validatesInput reports whether calls from this source get their params
// checked against the op's declared inputs. Only agent-driven sources: a
// workflow, schedule or test run keeps its historical lenient behaviour.
func validatesInput(src entity.ConnectorRunSource) bool {
	return src == entity.ConnectorRunSourceMCP || src == entity.ConnectorRunSourceCodeMode
}

// ValidateInput checks raw agent params against an operation's declared
// inputs. It returns nil when they fit. Only clear violations fail: a
// required field with no value and no default, a dropdown value outside its
// options, a number field that does not parse, a boolean field that is not
// a boolean. Values arrive either as JSON-typed (raw) or as strings.
func ValidateInput(op connector.Operation, raw map[string]any) *InputError {
	declared := make(map[string]bool, len(op.Input))
	var problems []string
	for _, f := range op.Input {
		declared[f.Key] = true
		v, present := raw[f.Key]
		s := ""
		if present {
			s = valueString(v)
		}
		if strings.TrimSpace(s) == "" {
			if f.Required && strings.TrimSpace(f.Value) == "" {
				problems = append(problems, fmt.Sprintf("%s is required", f.Key))
			}
			continue
		}
		switch f.Type {
		case "number":
			if _, ok := v.(float64); ok {
				break
			}
			if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
				problems = append(problems, fmt.Sprintf("%s must be a number, got %q", f.Key, clip(s)))
			}
		case "checkbox", "bool", "boolean":
			if _, ok := v.(bool); ok {
				break
			}
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "true", "false", "1", "0":
			default:
				problems = append(problems, fmt.Sprintf("%s must be true or false, got %q", f.Key, clip(s)))
			}
		case "dropdown":
			opts := optionList(f.Options)
			if len(opts) > 0 && !slices.Contains(opts, strings.TrimSpace(s)) {
				problems = append(problems, fmt.Sprintf("%s must be one of [%s], got %q", f.Key, strings.Join(opts, ", "), clip(s)))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	var unknown []string
	for k := range raw {
		if !declared[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return &InputError{Operation: op.Key, Problems: problems, Unknown: unknown, Inputs: op.Input}
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func optionList(raw string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == '|' || r == ',' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
