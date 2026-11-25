package slack

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
)

// WorkflowEventSink is the closure the slack channel calls for every
// inbound Slack event that should be routable as a workflow trigger.
// The `event` argument matches one of the descriptors registered in
// internal/agents/channels/slack/workflow (message, app_mention,
// block_action, view_submission, view_closed, shortcut, command,
// app_home_opened). Payload mirrors the corresponding event-payload
// struct in that subpackage, flattened to map[string]any so the
// workflow router can apply match rules generically.
//
// Production wiring: setup composer constructs a sink that adapts the
// (event, payload) pair into a workflow.Event and calls Router.Dispatch.
// Tests pass a closure that records calls.
type WorkflowEventSink func(ctx context.Context, event string, payload map[string]any)

// SetWorkflowEventSink wires the sink. Safe to call before Start (the
// channel just holds the closure until the first event arrives).
func (s *Channel) SetWorkflowEventSink(fn WorkflowEventSink) {
	s.cfgMu.Lock()
	s.workflowEmit = fn
	s.cfgMu.Unlock()
}

// HasWorkflowEventSink reports whether this instance's sink is wired.
//
// A nil sink makes every inbound Slack event a silent no-op on the
// workflow side, so "channel triggers stopped firing" looks identical to
// "no workflow matched". Exposed so setup can be asserted directly —
// each per-user instance needs its own wiring.
func (s *Channel) HasWorkflowEventSink() bool {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.workflowEmit != nil
}

// emitWorkflow fires the sink when set, no-op when nil. Always
// called from a goroutine that already holds — or doesn't need — the
// channel locks; emitWorkflow itself touches no Channel state besides
// the sink closure (read under cfgMu).
func (s *Channel) emitWorkflow(ctx context.Context, event string, payload map[string]any) {
	s.cfgMu.Lock()
	fn := s.workflowEmit
	s.cfgMu.Unlock()
	if fn == nil {
		return
	}
	fn(ctx, event, payload)
}

// emitInteractionWorkflow normalizes an InteractionCallback into one of
// the registered workflow event types (block_action, view_submission,
// view_closed, shortcut, message_action) and emits it. Each event's
// payload shape matches the matching struct in
// internal/agents/channels/slack/workflow.
//
// `raw` carries the full Slack payload verbatim so workflow nodes can
// reach fields not surfaced as a top-level key. Marshalling to JSON
// then unmarshalling to map[string]any keeps the shape JSON-friendly
// for downstream template engines and SSE serialisation.
func (s *Channel) emitInteractionWorkflow(ctx context.Context, cb slackgo.InteractionCallback) {
	switch cb.Type {
	case slackgo.InteractionTypeBlockActions:
		s.emitBlockAction(ctx, cb)
	case slackgo.InteractionTypeViewSubmission:
		s.emitViewSubmission(ctx, cb)
	case slackgo.InteractionTypeViewClosed:
		s.emitViewClosed(ctx, cb)
	case slackgo.InteractionTypeShortcut:
		s.emitShortcut(ctx, cb, "shortcut")
	case slackgo.InteractionTypeMessageAction:
		s.emitShortcut(ctx, cb, "message_action")
	}
}

func (s *Channel) emitBlockAction(ctx context.Context, cb slackgo.InteractionCallback) {
	if len(cb.ActionCallback.BlockActions) == 0 {
		return
	}
	a := cb.ActionCallback.BlockActions[0]
	payload := map[string]any{
		"user":         cb.User.ID,
		"action_id":    a.ActionID,
		"callback_id":  cb.CallbackID,
		"block_id":     a.BlockID,
		"value":        a.Value,
		"selected_opt": a.SelectedOption.Value,
		"channel_id":   cb.Channel.ID,
		"message_ts":   cb.Message.Timestamp,
		"thread":       threadKey(cb.Message.ThreadTimestamp, cb.Message.Timestamp),
		"trigger_id":   cb.TriggerID,
		"response_url": cb.ResponseURL,
		"state":        viewStateValues(cb.View),
		"raw":          rawMap(cb),
	}
	s.emitWorkflow(ctx, "block_action", payload)
}

func (s *Channel) emitViewSubmission(ctx context.Context, cb slackgo.InteractionCallback) {
	s.emitWorkflow(ctx, "view_submission", map[string]any{
		"user":             cb.User.ID,
		"callback_id":      cb.View.CallbackID,
		"view_id":          cb.View.ID,
		"view_hash":        cb.View.Hash,
		"private_metadata": cb.View.PrivateMetadata,
		"trigger_id":       cb.TriggerID,
		"values":           viewStateValues(cb.View),
		"raw":              rawMap(cb),
	})
}

func (s *Channel) emitViewClosed(ctx context.Context, cb slackgo.InteractionCallback) {
	s.emitWorkflow(ctx, "view_closed", map[string]any{
		"user":             cb.User.ID,
		"callback_id":      cb.View.CallbackID,
		"view_id":          cb.View.ID,
		"private_metadata": cb.View.PrivateMetadata,
	})
}

func (s *Channel) emitShortcut(ctx context.Context, cb slackgo.InteractionCallback, kind string) {
	payload := map[string]any{
		"user":        cb.User.ID,
		"type":        string(cb.Type),
		"callback_id": cb.CallbackID,
		"trigger_id":  cb.TriggerID,
	}
	if kind == "message_action" {
		payload["channel_id"] = cb.Channel.ID
		payload["message_ts"] = cb.Message.Timestamp
		if raw, err := json.Marshal(cb.Message); err == nil {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				payload["message_raw"] = m
			}
		}
	}
	s.emitWorkflow(ctx, kind, payload)
}

// viewStateValues flattens cb.View.State.Values (block_id → action_id
// → typed value) into a JSON-friendly nested map so workflow templates
// can read submitted form fields via
// `{{.Trigger.values.<block_id>.<action_id>.value}}`.
func viewStateValues(v slackgo.View) map[string]any {
	if v.State == nil || len(v.State.Values) == 0 {
		return nil
	}
	out := map[string]any{}
	for blockID, actions := range v.State.Values {
		bm := map[string]any{}
		for actionID, val := range actions {
			b, err := json.Marshal(val)
			if err != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				bm[actionID] = m
			}
		}
		out[blockID] = bm
	}
	return out
}

// rawMap returns the full InteractionCallback as a generic map so
// workflows can reach any field not lifted to a top-level payload key.
// Marshal failures fall back to nil — never blocks the dispatch.
func rawMap(cb slackgo.InteractionCallback) map[string]any {
	b, err := json.Marshal(cb)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// SetWorkflowWants wires the check "does an enabled workflow listen to
// this event" (nil = unknown, treated as no).
func (s *Channel) SetWorkflowWants(fn func(event string) bool) {
	s.cfgMu.Lock()
	s.workflowWants = fn
	s.cfgMu.Unlock()
}

func (s *Channel) workflowWanted(event string) bool {
	s.cfgMu.Lock()
	fn := s.workflowWants
	s.cfgMu.Unlock()
	return fn != nil && fn(event)
}

// reactedMessage is what a reaction workflow gets to know about the
// message that was reacted to.
type reactedMessage struct {
	text, thread, botID string
	at                  time.Time
}

// reactedCache keeps a fetched message for a while, shared by every bot
// instance: several reactions on one message (or several bots seeing the
// same reaction) cost one Slack call.
var reactedCache sync.Map // channel/ts -> reactedMessage

const reactedCacheTTL = 10 * time.Minute

// reactedMessageOf reads the reacted message (text, its thread, the bot
// that wrote it) with one conversations.replies call: Slack accepts a
// reply's ts there as well as a thread root's or a plain message's.
func (s *Channel) reactedMessageOf(ctx context.Context, channelID, ts string) (reactedMessage, bool) {
	key := channelID + "/" + ts
	if v, ok := reactedCache.Load(key); ok {
		if m := v.(reactedMessage); time.Since(m.at) < reactedCacheTTL {
			return m, true
		}
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return reactedMessage{}, false
	}
	msgs, _, _, err := api.GetConversationRepliesContext(ctx, &slackgo.GetConversationRepliesParameters{
		ChannelID: channelID, Timestamp: ts, Oldest: ts, Latest: ts, Inclusive: true, Limit: 2,
	})
	if err != nil {
		log.Debug().Str("channel", "slack").Err(err).Str("slack_channel", channelID).Str("ts", ts).
			Msg("reaction workflow: reacted message not readable; payload goes without its text")
		return reactedMessage{}, false
	}
	for _, m := range msgs {
		if m.Timestamp != ts {
			continue
		}
		out := reactedMessage{text: m.Text, thread: m.ThreadTimestamp, botID: m.BotID, at: time.Now()}
		if out.thread == "" {
			out.thread = ts
		}
		reactedCache.Store(key, out)
		return out, true
	}
	return reactedMessage{}, false
}

// emitReactionWorkflow fires slack.reaction / slack.reaction_removed.
// Slack pushes these, so a workflow reacting to an emoji needs no
// polling. Only when an enabled workflow listens to the event is the
// reacted message read (once, cached) so its text and thread ride along
// and can be filtered on. The bot's own reactions are skipped (loop
// guard), and the event key names the reactor and emoji so different
// reactions on one message are not collapsed into one.
func (s *Channel) emitReactionWorkflow(ctx context.Context, event, user, itemUser, reaction, channelID, ts string, added bool) {
	if user == "" || reaction == "" || channelID == "" || ts == "" || s.isBotUser(user) {
		return
	}
	key := "reaction:" + channelID + "/" + ts + "/" + reaction + "/" + user
	if !added {
		key += "/removed"
	}
	payload := map[string]any{
		"user":       user,
		"reaction":   reaction,
		"channel_id": channelID,
		"ts":         ts,
		"item_user":  itemUser,
		"added":      added,
		"is_dm":      len(channelID) > 0 && channelID[0] == 'D',
		"event_key":  key,
		"text":       "",
		"thread":     "",
		"is_reply":   false,
	}
	if s.workflowWanted(event) {
		if m, ok := s.reactedMessageOf(ctx, channelID, ts); ok {
			payload["text"] = m.text
			payload["thread"] = m.thread
			payload["is_reply"] = m.thread != ts
			payload["item_bot_id"] = m.botID
		}
	}
	s.emitWorkflow(ctx, event, payload)
}
