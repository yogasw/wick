// Package slack will hold the Slack transport: socket-mode (default)
// or HTTP Event API listener, reaction lifecycle, chunked replies,
// access control, and meta-command routing.
//
// Phase 5 work — placeholder folder so the transport sibling layout
// is established now.
//
// Reference (agents-design.md §4.7 "Implementasi Slack" + §10
// "Meta-Commands"):
//
//   - Listen modes: socket (persistent WebSocket, no public URL) /
//     http (HTTP Event API, requires public URL)
//   - Routing: thread_ts → SessionKey
//   - Access control: everyone | users | groups (user groups resolved
//     via Slack API per incoming message)
//   - Reaction lifecycle: ⏳ → ⚙️ → ✅ / 🚫 / ❌ on the user's message
//   - Final reply: 1 message per turn (buffered text_delta until
//     Done), chunked at 3800 chars
//   - Meta-commands: ganti agent / pakai project / reset / status /
//     dashboard / link / log — intercepted before forwarding to subprocess
//   - Rate limit: exponential backoff for chat.update tier 3 (50/min)
//
// When implemented, expect: listener.go (socket/HTTP), reactions.go
// (lifecycle), chunk.go (>4000 char split), access.go (mode matcher),
// metacmd.go (meta-command parser), transport.go (Transport interface
// satisfier).
package slack
