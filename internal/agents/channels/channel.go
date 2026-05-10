// Package channels provides message channel implementations for the Agents
// subsystem. Each channel (Slack, future: Telegram, Discord, etc.) satisfies
// the Channel interface and handles the full lifecycle:
//   - Listening for incoming messages
//   - Access control (channel-specific)
//   - Dispatching to the agent pool via a SendFunc
//   - Delivering responses back (reactions, threaded replies, SSE, etc.)
//
// Callers (server.go) wire a channel via Start(ctx) and Stop().
package channels

import "context"

// SendFunc is the signature the pool exposes for sending a user message
// into a session. Channels call this after passing access control and
// meta-command checks.
type SendFunc func(ctx context.Context, sessionID, agentName, source, role, text string) error

// Channel is the listener + responder contract every transport must satisfy.
// Name identifies the channel in logs and config. Start blocks until ctx is
// cancelled or a fatal error occurs. Stop signals a clean shutdown.
type Channel interface {
	Name() string
	Start(ctx context.Context) error
	Stop()
}

// IncomingMessage is the canonical inbound shape produced by each channel.
// SessionKey is the routing key (thread_ts for Slack, UUID for UI/API).
type IncomingMessage struct {
	SessionKey string   // routing key
	UserID     string   // sender identifier
	GroupIDs   []string // user groups (Slack only — access control)
	Text       string
	Source     string // "slack" | "ui" | "api"
	Raw        any    // original payload from the channel
}

// OutgoingMessage is what the channel delivers back to the user after an
// agent turn completes. Slack uses Text + State for reaction lifecycle;
// UI uses SSE which bypasses this struct entirely.
type OutgoingMessage struct {
	SessionKey string
	Text       string
	// State carries the reaction lifecycle marker for Slack:
	// "queued" | "running" | "done" | "blocked" | "error"
	State string
}
