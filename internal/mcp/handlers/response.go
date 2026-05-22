package handlers

import (
	"encoding/json"
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
