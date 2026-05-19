// Package channels provides message channel implementations for the Agents
// subsystem. Each channel (Slack, future: Telegram, Discord, etc.) satisfies
// the Channel interface and handles the full lifecycle:
//   - Listening for incoming messages
//   - Access control (channel-specific)
//   - Dispatching to the agent pool via a SendFunc
//   - Delivering responses back (reactions, threaded replies, SSE, etc.)
//
// Wiring is done by *Registry — server constructs channels with cfg only,
// hands them to the registry, and the registry attaches dependencies via
// optional setter interfaces and fans out events.
package channels

import (
	"context"
	"net/http"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
)

// SendFunc is the signature the pool exposes for sending a user message
// into a session. Channels call this after passing access control and
// meta-command checks.
type SendFunc func(ctx context.Context, sessionID, agentName, source, role, text string) error

// ApproveFn resolves a gate approval request originating from a channel.
// sessionID is the wick session, requestID is the gate request UUID,
// decision is one of the gate.Decision* constants. channelName is the
// originating channel name ("slack", "telegram", …) — passed for audit
// logging only. The registry wraps the user-supplied ApproveFn at Add
// time so each channel sees a 4-arg setter that already binds its name.
type ApproveFn func(sessionID, requestID, decision, matchKey string) error

// RegistryApproveFn is the multi-source variant the registry holds. It
// is wrapped per-channel into ApproveFn during Add so each channel keeps
// a 4-arg signature.
type RegistryApproveFn func(channelName, sessionID, requestID, decision, matchKey string) error

// SessionChecker reports whether a sessionID already exists. Implemented
// by *pool.Pool. Channels use it to decide whether the next inbound
// message starts a brand-new session — if so they prepend a one-time
// system turn (workspace/chat/user/link) so the agent's first reply is
// grounded.
type SessionChecker interface {
	SessionExists(sessionID string) bool
}

// SessionStartHook fires once when a channel sees a brand-new session
// (no on-disk state yet). Optional — channels that don't track session
// origin (e.g. UI, API) never fire it. ctxText is the human-readable
// origin metadata composed for the agent's first turn.
type SessionStartHook func(sessionID, source, ctxText string)

// Channel is the minimal contract every transport must satisfy. The
// registry routes everything else (event fan-out, reload, http handlers)
// through the optional interfaces below — implementing those is opt-in.
type Channel interface {
	Name() string
	Start(ctx context.Context) error
	Stop()
	IsConfigured() bool
}

// ── Optional setter interfaces ────────────────────────────────────────
// Registry.Add type-asserts each of these and calls the setter when the
// channel implements it. Channels declare exactly the dependencies they
// need; UI/API channels skip everything they don't use.

// SendFuncSetter receives the pool dispatch closure.
type SendFuncSetter interface{ SetSendFunc(SendFunc) }

// SessionCheckerSetter receives the session-exists probe.
type SessionCheckerSetter interface{ SetSessionChecker(SessionChecker) }

// SessionStartHookSetter receives the new-session callback.
type SessionStartHookSetter interface{ SetSessionStartHook(SessionStartHook) }

// ApproveFnSetter receives the gate approval resolver.
type ApproveFnSetter interface{ SetApproveFn(ApproveFn) }

// PublicURLSetter receives the public base URL for dashboard links.
// Slack uses this for /dashboard meta-command replies; Telegram doesn't.
type PublicURLSetter interface{ SetPublicURL(string) }

// ── Optional event-receiver interfaces ────────────────────────────────
// Registry.Dispatch* fans events out to whichever channels implement
// the matching interface. Slack and Telegram both opt in; UI uses SSE
// and ignores these entirely.

// AgentEventReceiver is fanned-out per agent event (TextDelta, Done, …).
type AgentEventReceiver interface {
	OnAgentEvent(sessionID string, ev event.AgentEvent)
}

// ApprovalReceiver is fanned-out for gate approval lifecycle.
type ApprovalReceiver interface {
	OnApprovalRequest(sessionID string, req gate.ApprovalRequest)
	OnApprovalResolved(sessionID, requestID, decision string)
}

// ── Workflow integration (opt-in) ─────────────────────────────────────
// Channels that want to be usable from the workflow editor implement
// these interfaces. Channels that don't (UI, API, REST one-shot) are
// invisible to the workflow channel/trigger pickers. Single source of
// truth — workflow package does not declare its own channel surface.

// WorkflowTriggerSpec describes one inbound event class the channel can
// fire as a workflow trigger. Surfaced via MCP for AI introspection +
// the editor's trigger-channel dropdown.
type WorkflowTriggerSpec struct {
	Type          string         `json:"type"` // always "channel"
	Events        []string       `json:"events"`
	Description   string         `json:"description"`
	MatchSchema   map[string]any `json:"match_schema,omitempty"`
	PayloadSchema map[string]any `json:"payload_schema,omitempty"`
}

// WorkflowActionSpec describes one outbound op a workflow channel-action
// node can invoke. Mirrors the input/output schema convention used by
// connector ops so the editor can render a typed args form.
type WorkflowActionSpec struct {
	ID           string         `json:"id"`
	Description  string         `json:"description"`
	Destructive  bool           `json:"destructive,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

// WorkflowTriggerProvider is implemented by channels that can fire
// workflow triggers (Slack, Telegram, …). Channels that only accept
// outbound calls (REST one-shot) skip this.
type WorkflowTriggerProvider interface {
	WorkflowTriggerSpecs() []WorkflowTriggerSpec
}

// WorkflowActionProvider is implemented by channels that expose
// outbound operations (Send, react, open_modal, …) to workflow action
// nodes.
type WorkflowActionProvider interface {
	WorkflowActionSpecs() []WorkflowActionSpec
	WorkflowSend(ctx context.Context, op string, args map[string]any) (any, error)
}

// WorkflowSessionOriginator reports whether this channel can be the
// origin of a multi-turn agent session. UI/Slack/Telegram return true;
// stateless transports (REST, one-shot webhook) return false. Workflow
// validator rejects channel triggers that need a reply path on
// channels that don't support sessions.
type WorkflowSessionOriginator interface {
	SupportsSession() bool
}

// LookupItem is one row returned by a picker lookup. ID is the stable
// identifier stored in the config; Name is the human label shown to the
// operator.
type LookupItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// HealthCheck is one row of an integration self-test (e.g. "auth.test ok",
// "users.list missing scope"). OK=true means the upstream call succeeded
// with the result the operator expects; Detail is a short human-readable
// note (scope hint, count, etc.).
type HealthCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// HealthChecker lets a channel expose a "Test Integration" probe that
// runs from the admin UI. Implementations should cover the API calls
// the channel relies on (auth, listing, search, write) so missing scopes
// surface before runtime.
type HealthChecker interface {
	HealthCheck() []HealthCheck
}

// LookupProvider lets a channel back picker fields with a live search
// against its upstream. Source is the registered key from the wick tag
// (e.g. "slack.users"). Implementations should cap results and skip
// deleted/bot entries.
type LookupProvider interface {
	Lookup(source, query string) ([]LookupItem, error)
}

// ── HTTP webhook + hot reload (opt-in) ────────────────────────────────

// HTTPHandlerProvider exposes a webhook handler the registry mounts on
// the public mux. Slack's HTTP-mode events use this; Telegram (long
// polling) does not.
type HTTPHandlerProvider interface {
	HTTPPath() string
	HTTPHandler() http.Handler
}

// MultiHTTPHandlerProvider extends HTTPHandlerProvider for channels
// that need to register more than one HTTP route (e.g. Slack registers
// both the inbound event webhook and a local send-message proxy).
type MultiHTTPHandlerProvider interface {
	HTTPHandlers() map[string]http.Handler
}

// ConfigSource is per-channel hot-reload glue. Hash returns a stable
// fingerprint of the currently-applied config; the registry watcher
// compares against the previous hash on each tick and calls Reload
// when it changes. Implementations decide where the config lives —
// see ConfigStore for the abstraction the bundled sources read from.
type ConfigSource interface {
	Hash() string
	Reload(ctx context.Context) error
}

// SlackConfigStore is the storage abstraction SlackConfigSource reads
// from. It hides the backend (DB, file, in-memory test fake) so the
// channels package can stay free of gorm imports inside the source
// implementations. Server wires a DB-backed implementor at boot.
type SlackConfigStore interface {
	LoadSlack() (cfg agentconfig.SlackChannelConfig, pubURL string, err error)
}

// TelegramConfigStore mirrors SlackConfigStore for Telegram.
type TelegramConfigStore interface {
	LoadTelegram() (cfg agentconfig.TelegramChannelConfig, err error)
}

// RestConfigStore mirrors SlackConfigStore for the OpenAI-compatible REST
// channel.
type RestConfigStore interface {
	LoadRest() (cfg agentconfig.RestChannelConfig, err error)
}

// ChannelEnsurer guarantees a default agent_channels row exists for the
// given channel type. setup composers call it before loading config so
// first-boot operators see the channel listed in the UI even when the
// row is empty.
type ChannelEnsurer interface {
	EnsureChannel(channelType string) error
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
