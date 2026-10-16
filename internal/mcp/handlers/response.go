package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// RPCRequest is the exported shape of a JSON-RPC 2.0 request passed
// from the mcp package into each handler function.
type RPCRequest struct {
	ID     json.RawMessage
	Params json.RawMessage
}

// Responder carries the two write helpers the mcp package exposes to
// handler functions. Populated once per tools/call dispatch and passed
// down to every handler.
type Responder struct {
	// WriteResult writes a successful JSON-RPC result envelope.
	WriteResult func(w http.ResponseWriter, id json.RawMessage, result any)
	// WriteError writes a JSON-RPC error envelope (transport-level).
	WriteError func(w http.ResponseWriter, id json.RawMessage, code int, message string, data any)
}

// ToolJSON marshals payload and writes a successful tool result.
func (r Responder) ToolJSON(w http.ResponseWriter, id json.RawMessage, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		r.WriteError(w, id, -32603, "marshal: "+err.Error(), nil)
		return
	}
	r.WriteResult(w, id, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(body)}},
		IsError: false,
	})
}

// ToolError writes a tool result with isError=true and a JSON error body.
func (r Responder) ToolError(w http.ResponseWriter, id json.RawMessage, message, toolID string) {
	body := map[string]string{"error": message}
	if toolID != "" {
		body["tool_id"] = toolID
	}
	b, _ := json.Marshal(body)
	r.WriteResult(w, id, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
		IsError: true,
	})
}

// MaxToolResultBytes caps the text of one wick_execute result. A bigger
// message closes the client's MCP connection instead of arriving, so the
// call fails with a readable tool error instead.
const MaxToolResultBytes = 6 << 20

// OversizedResult returns the tool error for a result text over
// MaxToolResultBytes, or "" when it fits. The size is measured as the text
// will travel: JSON-escaped inside the response envelope.
func OversizedResult(text string) string {
	n := escapedLen(text)
	if n <= MaxToolResultBytes {
		return ""
	}
	return fmt.Sprintf("result too large (%.1f MB, limit %d MB); it was not sent because a message this big closes the connection. Narrow the request (fewer rows, a filter, a smaller page), split a batch into smaller ones, or use the op's file option when it has one (e.g. save_to_artifacts) so the content is written to artifacts/ and only the path comes back", float64(n)/(1<<20), MaxToolResultBytes>>20)
}

// escapedLen is len(text) once encoded as a JSON string (without quotes),
// as encoding/json writes it: quote, backslash and the common control
// characters take 2 bytes, other control characters and <, >, & take 6.
func escapedLen(text string) int {
	n := len(text)
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '"' || c == '\\' || c == '\n' || c == '\r' || c == '\t':
			n++
		case c < 0x20 || c == '<' || c == '>' || c == '&':
			n += 5
		}
	}
	return n
}
