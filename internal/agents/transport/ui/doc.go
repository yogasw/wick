// Package ui will hold the UI transport: an HTTP handler glue layer
// that turns POST /tools/agents/sessions/{id}/send into an
// IncomingMessage and pumps it through the pool.
//
// Phase 4 work — placeholder folder so the transport sibling layout
// is established now.
//
// Reference (agents-design.md §4.7 "Implementasi UI" + §9.2 "Manager
// Tool"):
//
//   - Handler builds IncomingMessage{SessionKey: id, Text: text,
//     Source: "ui", UserID: <wick-user>}
//   - Auth via wick session login (not Slack user ID)
//   - Output via SSE broadcast — the Send method is a no-op for UI
//     because dashboard updates flow through transport/sse, not the
//     Transport.Send path
//   - Modes: user (role=user) and system (role=system, operator
//     instruction)
//
// When implemented, expect: handler.go (HTTP wiring), transport.go
// (Transport interface satisfier), test with httptest.
package ui
