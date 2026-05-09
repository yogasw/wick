// Package transport defines the boundary between Agents and message
// sources (Slack, UI, future API). The pool consumes IncomingMessage
// values; transports produce them.
//
// Skeleton landed in phase 2 so the per-transport sibling layout
// (transport/ui, transport/slack, transport/api) is established now —
// phase 4 fills in ui, phase 5 fills in slack, api stays a placeholder
// until there's a real consumer.
//
// Why an interface here even though pool/Send is already string-based:
// transports own routing-key derivation (Slack thread_ts vs UUID),
// access control (Slack only — see §4.7), and how outbound responses
// are delivered (reaction lifecycle vs SSE broadcast vs raw HTTP).
// Putting that behind a Transport keeps pool free of transport-
// specific hacks.
package transport

import "context"

// IncomingMessage is the canonical shape every transport produces.
// SessionKey is the routing key (thread_ts for slack, UUID for ui/api);
// the pool / session manager treats it as the session ID.
//
// UserID and GroupIDs are populated for Slack (used by access control
// in §4.7); other transports may leave them empty.
type IncomingMessage struct {
	SessionKey string   // routing key — thread_ts (slack) or UUID (ui/api)
	UserID     string   // sender (slack user ID or wick user ID)
	GroupIDs   []string // user groups (slack only — access check)
	Text       string
	Source     string // "slack" | "ui" | "api"
	Raw        any    // payload original from transport
}

// OutgoingMessage is what the agent pipeline hands back when there is
// something to deliver to the user. Slack uses Text + reaction state;
// UI uses SSE which doesn't go through this struct (see transport/ui
// when phase 4 lands). The shape is intentionally minimal — extend as
// new fields are needed.
type OutgoingMessage struct {
	SessionKey string
	Text       string
	// State carries the Slack reaction lifecycle marker (queued /
	// running / done / blocked / error). Empty when the transport
	// doesn't use reactions.
	State string
}

// MessageHandler is what the pool registers with each Transport. The
// transport calls handler for every IncomingMessage it produces; the
// handler pushes into the pool's Send pipeline.
type MessageHandler func(ctx context.Context, msg IncomingMessage) error

// Transport is the listener half. Listen blocks until ctx is canceled,
// pumping incoming messages to handler. Send is the outbound half — a
// transport may stub it (UI relies on SSE, not this method) but Slack
// uses it.
type Transport interface {
	Listen(ctx context.Context, handler MessageHandler) error
	Send(ctx context.Context, msg OutgoingMessage) error
}
