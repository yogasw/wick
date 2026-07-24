package wick

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// sessionIDFromDir derives the wick session id from the session dir —
// Layout.SessionDir(id) = <sessions>/<id>, so the id is the base name.
// Session-aware MCP tools (ask_user, wick_session_*) key on this id.
func sessionIDFromDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Base(dir)
}

// newSessionID mints a stream-json session id for a spawn. wick resume
// is a store rebuild, so this id is only the parser's SessionStart
// marker — uniqueness is all that matters.
func newSessionID() string { return uuid.NewString() }

// marshalArgs renders a tool call's args map as a compact JSON object
// for the tool_use line. nil/empty → "{}".
func marshalArgs(args map[string]any) json.RawMessage {
	if len(args) == 0 {
		return json.RawMessage("{}")
	}
	b, err := json.Marshal(args)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// vendorErrorMessage turns a raw model/transport error into a concise,
// user-actionable sentence for the UI Error bubble. Recognises the
// common fatal cases (bad key, bad model) so the operator knows to fix
// the model config rather than staring at a stack-trace-shaped string.
func vendorErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "401") || strings.Contains(low, "unauthorized") || strings.Contains(low, "api key") || strings.Contains(low, "invalid_api_key"):
		return "Model rejected the API key (401). Open Providers → Wick and re-enter the key for this model.\n\n" + msg
	case strings.Contains(low, "404") || strings.Contains(low, "model_not_found") || strings.Contains(low, "does not exist"):
		return "Model id not found (404). Check the model id in Providers → Wick.\n\n" + msg
	case strings.Contains(low, "429") || strings.Contains(low, "rate limit") || strings.Contains(low, "quota"):
		return "Rate limited by the provider (429) after retries. Try again shortly or raise your quota.\n\n" + msg
	default:
		return "Model call failed: " + msg
	}
}
