package slack

import (
	"context"
	"fmt"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// WorkflowTriggerSpecs declares the inbound event classes the Slack
// channel emits into the workflow router.
func (s *Channel) WorkflowTriggerSpecs() []agentchannels.WorkflowTriggerSpec {
	return []agentchannels.WorkflowTriggerSpec{{
		Type:        "channel",
		Events:      []string{"message", "action", "submission", "reaction", "mention"},
		Description: "Slack events forwarded by the wick Slack channel adapter.",
		MatchSchema: map[string]any{
			"properties": map[string]any{
				"keywords":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"regex":             map[string]any{"type": "string"},
				"mention_bot":       map[string]any{"type": "boolean"},
				"from_threads_only": map[string]any{"type": "boolean"},
			},
		},
	}}
}

// WorkflowActionSpecs declares the outbound op catalog for Slack action
// nodes. Schemas match what WorkflowSend accepts.
func (s *Channel) WorkflowActionSpecs() []agentchannels.WorkflowActionSpec {
	return []agentchannels.WorkflowActionSpec{
		{
			ID:          "send_message",
			Description: "Post plain message to Slack channel. Returns posted ts.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"channel":   map[string]any{"type": "string"},
					"thread_ts": map[string]any{"type": "string"},
					"text":      map[string]any{"type": "string"},
				},
				"required": []any{"channel", "text"},
			},
			OutputSchema: map[string]any{
				"properties": map[string]any{
					"ts":      map[string]any{"type": "string"},
					"channel": map[string]any{"type": "string"},
				},
			},
		},
		{
			ID:          "reply_thread",
			Description: "Reply to existing Slack thread. Posts message with thread_ts set.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"channel": map[string]any{"type": "string"},
					"thread":  map[string]any{"type": "string"},
					"text":    map[string]any{"type": "string"},
				},
				"required": []any{"channel", "thread", "text"},
			},
		},
		{
			ID:          "send_dm",
			Description: "Send a direct message to a Slack user.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"user": map[string]any{"type": "string"},
					"text": map[string]any{"type": "string"},
				},
				"required": []any{"user", "text"},
			},
		},
		{
			ID:          "react",
			Description: "Add an emoji reaction to a message. Idempotent.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"channel":    map[string]any{"type": "string"},
					"message_ts": map[string]any{"type": "string"},
					"emoji":      map[string]any{"type": "string"},
				},
				"required": []any{"channel", "message_ts", "emoji"},
			},
		},
		{
			ID:          "update_message",
			Description: "Edit a posted Slack message.",
			Destructive: true,
			InputSchema: map[string]any{
				"properties": map[string]any{
					"channel": map[string]any{"type": "string"},
					"ts":      map[string]any{"type": "string"},
					"text":    map[string]any{"type": "string"},
				},
				"required": []any{"channel", "ts", "text"},
			},
		},
	}
}

// SupportsSession reports that Slack can originate multi-turn agent
// sessions (thread = session boundary).
func (s *Channel) SupportsSession() bool { return true }

// WorkflowSend dispatches one outbound op against the live Slack API.
// Stub implementation — returns an error if the channel isn't running
// (cfg empty or Start() not called). Full per-op Block Kit payload
// wiring lands when the action-node UI form is built; for now the
// dispatcher confirms the registry path works.
func (s *Channel) WorkflowSend(ctx context.Context, op string, args map[string]any) (any, error) {
	if !s.IsConfigured() {
		return nil, fmt.Errorf("slack channel not configured")
	}
	specs := s.WorkflowActionSpecs()
	known := false
	for _, sp := range specs {
		if sp.ID == op {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("slack: unknown op %q", op)
	}
	// TODO(workflow): wire to real slack API client via s.client once
	// the action-node form is finalized. Returning args echo keeps the
	// integration path testable end-to-end.
	return map[string]any{"echo": args, "op": op}, nil
}
