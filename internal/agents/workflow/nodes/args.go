package nodes

import (
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

// renderArgsWithModes renders a node's args map honouring the per-key
// mode hint. Mode "fixed" keeps the value literal — useful when the
// operator wants a `{{` substring to survive into the request without
// being interpreted as a Go template. Mode "expression" (or empty,
// for backward-compat with pre-mode workflows) sends the value
// through template.RenderInto against the run context.
//
// Returns a fresh map[string]any whose keys mirror args; nested
// values inside expression-mode entries pass through RenderInto
// recursively. Fixed-mode entries are deep-copied verbatim so the
// executor can safely stringify/mutate them.
func renderArgsWithModes(args map[string]any, modes map[string]string, rc *workflow.RunContext) (map[string]any, error) {
	if len(args) == 0 {
		return map[string]any{}, nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if modes[k] == "fixed" {
			out[k] = v
			continue
		}
		rendered, err := template.RenderInto(v, rc.RenderCtx())
		if err != nil {
			return nil, &renderKeyError{Key: k, Err: err}
		}
		out[k] = rendered
	}
	return out, nil
}

// renderKeyError adds the failing arg key to a template render
// failure so the engine can report "at key X" without each executor
// re-wrapping the error.
type renderKeyError struct {
	Key string
	Err error
}

func (e *renderKeyError) Error() string {
	return "at key \"" + e.Key + "\": " + e.Err.Error()
}

func (e *renderKeyError) Unwrap() error { return e.Err }
