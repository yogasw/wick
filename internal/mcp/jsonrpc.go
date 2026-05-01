// Package mcp implements the Model Context Protocol JSON-RPC 2.0
// transport that LLM clients (Claude Desktop, Cursor, Claude.ai web)
// call to discover and invoke wick connectors as tools.
//
// Surface:
//
//	POST /mcp                                  — JSON-RPC requests
//	GET  /.well-known/oauth-protected-resource — auth metadata (RFC 9728)
//
// Methods served (server-side, JSON-RPC 2.0):
//
//	initialize      — protocol handshake, capability negotiation
//	tools/list      — enumerate the caller's accessible connector ops
//	tools/call      — invoke one op, dispatching to connectors.Service.Execute
//	notifications/* — accepted as no-ops (we don't push from the server yet)
//
// Auth model:
//
//	Static bearer (wick_pat_...) — PAT path, decoded via accesstoken.Service
//	OAuth opaque token           — OAuth path, decoded via oauth.Service
//	Anything else                — 401 with WWW-Authenticate pointing at
//	                                /.well-known/oauth-protected-resource
//
// The auth middleware resolves a user_id and the user's filter-tag IDs
// onto the request context; everything below it (tools/list,
// tools/call) sees only the connectors that user can access.
package mcp

import (
	"encoding/json"
	"net/http"
)

// rpcRequest is one JSON-RPC 2.0 inbound request envelope.
//
// ID is RawMessage so the server can echo it verbatim — the spec
// allows string, number, or null IDs, and "null" is reserved for
// notifications (no response expected).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the success envelope. Result is interface{} so each
// method can return a typed payload that json.Marshal handles.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// rpcErrorResponse is the failure envelope. Code/message follow the
// JSON-RPC 2.0 error code conventions; Data is optional structured
// payload for debugging.
type rpcErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   rpcError        `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes (https://www.jsonrpc.org/specification#error_object).
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// isNotification reports whether the request is a notification — i.e.
// the client expects no response. Per JSON-RPC 2.0 a missing or null
// id marks a notification.
func (r rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// writeRPCResult marshals a successful JSON-RPC 2.0 response.
func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// writeRPCError marshals a JSON-RPC 2.0 error response. Always returns
// 200 OK at the HTTP layer — the spec puts the error inside the body.
func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string, data any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	resp := rpcErrorResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   rpcError{Code: code, Message: message, Data: data},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
