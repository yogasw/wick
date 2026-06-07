package agents

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
)

// subBuffer is per-subscriber channel depth. Sized for tool_use bursts
// where a single agent turn can fire 20-50 events back-to-back; 256
// gives slow clients (browser tab backgrounded, slow network) room to
// catch up before we start dropping. Drop-on-full is still the policy —
// a stuck client must never stall the agent reader goroutine.
const subBuffer = 256

// Event is one SSE payload pushed to browser subscribers. Type
// distinguishes agent stream events ("text_delta", "tool_use", ...)
// from lifecycle events ("lifecycle"); the latter carry PID +
// lifecycle label in Data so the UI can update the status badge
// without re-fetching the page.
type Event struct {
	SessionID string `json:"session_id"`
	AgentName string `json:"agent_name"`
	Type      string `json:"type"`
	Data      string `json:"data"`
	// ToolName, ToolInput, ToolUseID are populated for tool_use events;
	// ToolUseID and IsError are also set for tool_result events.
	ToolName  string `json:"tool_name,omitempty"`
	ToolInput string `json:"tool_input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Lifecycle string `json:"lifecycle,omitempty"`
	// At / EndAt carry Unix ms timestamps for tool_use/tool_result events
	// so the UI can show "started HH:MM:SS, took Ns".
	At    int64 `json:"at,omitempty"`
	EndAt int64 `json:"end_at,omitempty"`
}

func (e Event) JSON() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Broadcaster fans out agent events to all subscribed SSE connections.
// Subscribe returns a receive channel and an unsub func. Publish is
// called from ClaudeFactory.OnEvent on every AgentEvent.
//
// subs is keyed by sessionID ("" = global subscribers that receive all
// events). Channels are buffered at 64 so a slow client never stalls
// the agent reader goroutine.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[string][]chan Event
}

// NewBroadcaster returns a ready Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[string][]chan Event)}
}

// Subscribe registers a listener for a specific session (or "" for all).
// The caller must call the returned unsub func when the SSE connection closes.
func (b *Broadcaster) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	b.mu.Lock()
	b.subs[sessionID] = append(b.subs[sessionID], ch)
	b.mu.Unlock()
	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[sessionID]
		for i, c := range list {
			if c == ch {
				b.subs[sessionID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		close(ch)
	}
	return ch, unsub
}

// Publish fires ev to all subscribers of sessionID and all global ("") subscribers.
// Non-blocking: a full channel's event is dropped rather than blocking.
func (b *Broadcaster) Publish(sessionID, agentName string, ev event.AgentEvent) {
	payload := Event{
		SessionID: sessionID,
		AgentName: agentName,
		Type:      ev.Type.String(),
		Data:      ev.Text,
	}
	now := time.Now().UnixMilli()
	switch ev.Type {
	case event.ToolUse:
		payload.Data = ev.ToolName
		payload.ToolName = ev.ToolName
		payload.ToolInput = ev.ToolInput
		payload.ToolUseID = ev.ToolUseID
		payload.At = now
	case event.ToolResult:
		payload.Data = ev.Text
		payload.ToolUseID = ev.ToolUseID
		payload.IsError = ev.IsError
		payload.At = now
	case event.Thinking:
		payload.At = now
	case event.Error:
		payload.Data = ev.ErrorMsg
	}
	log.Debug().
		Str("session", sessionID).
		Str("agent", agentName).
		Str("event_type", payload.Type).
		Str("data", payload.Data).
		Str("tool_name", payload.ToolName).
		Str("tool_use_id", payload.ToolUseID).
		Msg("sse.publish: broadcasting event")
	b.fanout(sessionID, payload)
}

// PublishLifecycle pushes a lifecycle transition (Spawning, Killed)
// to subscribers. Idle/Working transitions are inferred from
// AgentEvent flow on the client side; only the bookend transitions —
// which never carry an AgentEvent — go through this channel.
// PublishLifecycle takes the spawn-time ctx so the broadcast log line
// carries the originating request_id (set by the HTTP middleware) when
// the spawn came from an HTTP path. Pass context.Background() when
// no spawn ctx is in scope.
func (b *Broadcaster) PublishLifecycle(ctx context.Context, sessionID, agentName, lifecycle string, pid int) {
	log.Ctx(ctx).Debug().
		Str("component", "sse").
		Str("session", sessionID).
		Str("agent", agentName).
		Str("lifecycle", lifecycle).
		Int("pid", pid).
		Msg("sse.publish: broadcasting lifecycle")
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		AgentName: agentName,
		Type:      "lifecycle",
		Lifecycle: lifecycle,
		PID:       pid,
	})
}

// PublishGitStatusJSON broadcasts a pre-marshalled git_status payload to
// a session's subscribers. The payload is the full repo+status snapshot
// (built by the fs watcher) so the FE updates entirely from the event —
// no follow-up fetch, hence no polling. The marshalling lives in the
// caller (scm_watch.go) to avoid an import cycle on the scm types.
func (b *Broadcaster) PublishGitStatusJSON(sessionID, jsonPayload string) {
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		Type:      "git_status",
		Data:      jsonPayload,
	})
}

// PublishApprovalRequest fires when the gate binary dials the daemon socket
// with an unrecognised command. Browsers render this as a modal with
// 4 decision buttons (approve_once / approve_session / approve_always
// / block); the user's pick rides back through POST /approve.
//
// Data is the JSON-encoded ApprovalRequest so the front-end can
// decode it once and use every field (cmd, work_dir, match_key, ...).
func (b *Broadcaster) PublishApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	body, err := json.Marshal(req)
	if err != nil {
		log.Warn().Err(err).Str("session_id", sessionID).Msg("approval request marshal failed")
		return
	}
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		AgentName: req.AgentName,
		Type:      "approval_request",
		Data:      string(body),
	})
}

// PublishApprovalResolved fires once a decision is delivered (UI
// click, timeout, or listener close). Browsers use this to dismiss
// any open modal across all tabs subscribed to the session.
func (b *Broadcaster) PublishApprovalResolved(sessionID, requestID, decision string) {
	body, _ := json.Marshal(map[string]string{
		"id":       requestID,
		"decision": decision,
	})
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		Type:      "approval_resolved",
		Data:      string(body),
	})
}

// PublishAskUser fires when the ask_user MCP tool is invoked by an
// agent. Front-end renders an inline card with options + freeform
// input; user's pick rides back through POST /answer. Data is the
// JSON-encoded request body so every field (question, options,
// allow_freeform, ...) round-trips exactly once.
func (b *Broadcaster) PublishAskUser(sessionID, agentName string, payload []byte) {
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		AgentName: agentName,
		Type:      "ask_user",
		Data:      string(payload),
	})
}

// PublishAskUserResolved fires once an ask_user request resolves
// (UI answer or timeout). Used by the UI to dismiss the inline card
// across all tabs subscribed to the session.
func (b *Broadcaster) PublishAskUserResolved(sessionID, requestID string) {
	body, _ := json.Marshal(map[string]string{"id": requestID})
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		Type:      "ask_user_resolved",
		Data:      string(body),
	})
}

// PublishRaw fires an arbitrary typed SSE event. Used to inject synthetic
// agent events (e.g. text_delta + done for a switch confirmation reply).
// PoolStatsPayload is the JSON shape of a pool_stats SSE event.
// Sent to global ("") subscribers on every lifecycle transition so
// the Providers page can update the Active Processes panel without reload.
type PoolStatsPayload struct {
	Active        int                `json:"active"`
	Max           int                `json:"max"`
	QueueLen      int                `json:"queue_len"`
	LiveProcesses []LiveProcessEntry `json:"live_processes"`
}

// LiveProcessEntry is one row in PoolStatsPayload.
type LiveProcessEntry struct {
	SessionID string `json:"session_id"`
	AgentName string `json:"agent_name"`
	Provider  string `json:"provider,omitempty"` // "type/name"
	PID       int    `json:"pid,omitempty"`
	Queued    int    `json:"queued,omitempty"` // messages waiting after current turn
	Alive     bool   `json:"alive"`            // false only for a genuinely dead process (zombie). Respawn-mode idle-between-turns is alive.
	Lifecycle string `json:"lifecycle"`
	Substate  string `json:"substate,omitempty"`
}

// PublishPoolStats broadcasts a pool_stats event to all global SSE
// subscribers (sessionID == ""). Called after every lifecycle
// transition so the Providers page stays live.
func (b *Broadcaster) PublishPoolStats(active, max, queueLen int, procs []LiveProcessEntry) {
	body, _ := json.Marshal(PoolStatsPayload{
		Active:        active,
		Max:           max,
		QueueLen:      queueLen,
		LiveProcesses: procs,
	})
	b.fanout("", Event{
		Type: "pool_stats",
		Data: string(body),
	})
}

func (b *Broadcaster) PublishRaw(sessionID, agentName, evType, data string) {
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		AgentName: agentName,
		Type:      evType,
		Data:      data,
	})
}

// PublishSystemTurn fires a system_turn event so the UI can append it
// to the conversation without a page reload. Data is JSON with text +
// steps so the front-end can render the pill + checklist inline.
func (b *Broadcaster) PublishSystemTurn(sessionID, agentName, text string, steps []string) {
	body, _ := json.Marshal(map[string]any{
		"text":  text,
		"steps": steps,
	})
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		AgentName: agentName,
		Type:      "system_turn",
		Data:      string(body),
	})
}

func (b *Broadcaster) fanout(sessionID string, payload Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, key := range []string{sessionID, ""} {
		for _, ch := range b.subs[key] {
			select {
			case ch <- payload:
			default:
				log.Warn().
					Str("session_id", payload.SessionID).
					Str("agent", payload.AgentName).
					Str("event_type", payload.Type).
					Int("buffer", subBuffer).
					Msg("sse: subscriber buffer full, dropping event")
			}
		}
	}
}
