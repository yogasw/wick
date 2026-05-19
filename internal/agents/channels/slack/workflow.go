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
		{
			ID:          "open_modal",
			Description: "Open a modal in response to a block_action / shortcut / command. trigger_id expires in 3 seconds.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"trigger_id": map[string]any{"type": "string"},
					"view":       map[string]any{"type": "string"},
				},
				"required": []any{"trigger_id", "view"},
			},
		},
		{
			ID:          "update_modal",
			Description: "Replace the contents of an open modal. Pass view_id from open_modal output.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"view_id":   map[string]any{"type": "string"},
					"view_hash": map[string]any{"type": "string"},
					"view":      map[string]any{"type": "string"},
				},
				"required": []any{"view_id", "view"},
			},
		},
		{
			ID:          "push_modal",
			Description: "Push a new view onto the modal stack. trigger_id expires in 3 seconds.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"trigger_id": map[string]any{"type": "string"},
					"view":       map[string]any{"type": "string"},
				},
				"required": []any{"trigger_id", "view"},
			},
		},
		{
			ID:          "open_dm",
			Description: "Open a DM channel with a user and return the channel ID.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"user": map[string]any{"type": "string"},
				},
				"required": []any{"user"},
			},
		},
		{
			ID:          "send_ephemeral",
			Description: "Send an ephemeral message visible only to one user.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"channel": map[string]any{"type": "string"},
					"user":    map[string]any{"type": "string"},
					"text":    map[string]any{"type": "string"},
				},
				"required": []any{"channel", "user", "text"},
			},
		},
		{
			ID:          "publish_home",
			Description: "Publish a Home tab view for a user.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"user_id": map[string]any{"type": "string"},
					"view":    map[string]any{"type": "string"},
				},
				"required": []any{"user_id", "view"},
			},
		},
		{
			ID:          "respond_url",
			Description: "Post a response to a slash command or interactive payload via response_url.",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"response_url":      map[string]any{"type": "string"},
					"text":              map[string]any{"type": "string"},
					"replace_original":  map[string]any{"type": "boolean"},
					"delete_original":   map[string]any{"type": "boolean"},
					"response_type":     map[string]any{"type": "string"},
				},
				"required": []any{"response_url"},
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
