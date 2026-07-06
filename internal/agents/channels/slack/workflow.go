package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// WorkflowTriggerSpecs declares the inbound event classes the Slack
// channel emits into the workflow router.
func (s *Channel) WorkflowTriggerSpecs() []agentchannels.WorkflowTriggerSpec {
	return []agentchannels.WorkflowTriggerSpec{{
		Type:        "channel",
		Events:      []string{"message", "thread_started", "action", "submission", "reaction", "mention"},
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
					"response_url":     map[string]any{"type": "string"},
					"text":             map[string]any{"type": "string"},
					"replace_original": map[string]any{"type": "boolean"},
					"delete_original":  map[string]any{"type": "boolean"},
					"response_type":    map[string]any{"type": "string"},
				},
				"required": []any{"response_url"},
			},
		},
	}
}

// SupportsSession reports that Slack can originate multi-turn agent
// sessions (thread = session boundary).
func (s *Channel) SupportsSession() bool { return true }

// slackWorkflowAPI is the subset of *slackgo.Client that outbound
// workflow actions use. Narrowed to an interface so workflowSend is
// unit-testable with a fake; *slackgo.Client satisfies it directly.
type slackWorkflowAPI interface {
	PostMessageContext(ctx context.Context, channelID string, options ...slackgo.MsgOption) (string, string, error)
	PostEphemeralContext(ctx context.Context, channelID, userID string, options ...slackgo.MsgOption) (string, error)
	UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slackgo.MsgOption) (string, string, string, error)
	AddReactionContext(ctx context.Context, name string, item slackgo.ItemRef) error
	OpenConversationContext(ctx context.Context, params *slackgo.OpenConversationParameters) (*slackgo.Channel, bool, bool, error)
	OpenViewContext(ctx context.Context, triggerID string, view slackgo.ModalViewRequest) (*slackgo.ViewResponse, error)
	PushViewContext(ctx context.Context, triggerID string, view slackgo.ModalViewRequest) (*slackgo.ViewResponse, error)
	UpdateViewContext(ctx context.Context, view slackgo.ModalViewRequest, externalID, hash, viewID string) (*slackgo.ViewResponse, error)
	PublishViewContext(ctx context.Context, req slackgo.PublishViewContextRequest) (*slackgo.ViewResponse, error)
}

// WorkflowSend dispatches one outbound op against the live Slack API.
func (s *Channel) WorkflowSend(ctx context.Context, op string, args map[string]any) (any, error) {
	if !s.IsConfigured() {
		return nil, fmt.Errorf("slack channel not configured")
	}
	return s.workflowSend(ctx, s.api, op, args)
}

// workflowSend is the api-injectable core of WorkflowSend.
func (s *Channel) workflowSend(ctx context.Context, api slackWorkflowAPI, op string, args map[string]any) (any, error) {
	switch op {
	case "send_message":
		if err := requireArgs(op, args, "channel", "text"); err != nil {
			return nil, err
		}
		opts := []slackgo.MsgOption{slackgo.MsgOptionText(argString(args, "text"), false)}
		if ts := argString(args, "thread_ts"); ts != "" {
			opts = append(opts, slackgo.MsgOptionTS(ts))
		}
		ch, ts, err := api.PostMessageContext(ctx, argString(args, "channel"), opts...)
		if err != nil {
			return nil, fmt.Errorf("slack send_message: %w", err)
		}
		return map[string]any{"ts": ts, "channel": ch}, nil

	case "reply_thread":
		if err := requireArgs(op, args, "channel", "thread", "text"); err != nil {
			return nil, err
		}
		ch, ts, err := api.PostMessageContext(ctx, argString(args, "channel"),
			slackgo.MsgOptionText(argString(args, "text"), false),
			slackgo.MsgOptionTS(argString(args, "thread")))
		if err != nil {
			return nil, fmt.Errorf("slack reply_thread: %w", err)
		}
		return map[string]any{"ts": ts, "channel": ch}, nil

	case "send_dm":
		if err := requireArgs(op, args, "user", "text"); err != nil {
			return nil, err
		}
		chID, err := openDM(ctx, api, argString(args, "user"))
		if err != nil {
			return nil, err
		}
		_, ts, err := api.PostMessageContext(ctx, chID, slackgo.MsgOptionText(argString(args, "text"), false))
		if err != nil {
			return nil, fmt.Errorf("slack send_dm: %w", err)
		}
		return map[string]any{"ts": ts, "channel": chID}, nil

	case "send_ephemeral":
		if err := requireArgs(op, args, "channel", "user", "text"); err != nil {
			return nil, err
		}
		ts, err := api.PostEphemeralContext(ctx, argString(args, "channel"), argString(args, "user"),
			slackgo.MsgOptionText(argString(args, "text"), false))
		if err != nil {
			return nil, fmt.Errorf("slack send_ephemeral: %w", err)
		}
		return map[string]any{"ts": ts}, nil

	case "react":
		if err := requireArgs(op, args, "channel", "message_ts", "emoji"); err != nil {
			return nil, err
		}
		if err := api.AddReactionContext(ctx, argString(args, "emoji"), slackgo.ItemRef{
			Channel:   argString(args, "channel"),
			Timestamp: argString(args, "message_ts"),
		}); err != nil {
			return nil, fmt.Errorf("slack react: %w", err)
		}
		return map[string]any{"ok": true}, nil

	case "update_message":
		if err := requireArgs(op, args, "channel", "ts", "text"); err != nil {
			return nil, err
		}
		ch, ts, _, err := api.UpdateMessageContext(ctx, argString(args, "channel"), argString(args, "ts"),
			slackgo.MsgOptionText(argString(args, "text"), false))
		if err != nil {
			return nil, fmt.Errorf("slack update_message: %w", err)
		}
		return map[string]any{"ts": ts, "channel": ch}, nil

	case "open_dm":
		if err := requireArgs(op, args, "user"); err != nil {
			return nil, err
		}
		chID, err := openDM(ctx, api, argString(args, "user"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"channel": chID}, nil

	case "open_modal":
		if err := requireArgs(op, args, "trigger_id", "view"); err != nil {
			return nil, err
		}
		view, err := parseModalView(args)
		if err != nil {
			return nil, err
		}
		resp, err := api.OpenViewContext(ctx, argString(args, "trigger_id"), view)
		if err != nil {
			return nil, fmt.Errorf("slack open_modal: %w", err)
		}
		return map[string]any{"view_id": resp.View.ID, "view_hash": resp.View.Hash}, nil

	case "push_modal":
		if err := requireArgs(op, args, "trigger_id", "view"); err != nil {
			return nil, err
		}
		view, err := parseModalView(args)
		if err != nil {
			return nil, err
		}
		resp, err := api.PushViewContext(ctx, argString(args, "trigger_id"), view)
		if err != nil {
			return nil, fmt.Errorf("slack push_modal: %w", err)
		}
		return map[string]any{"view_id": resp.View.ID, "view_hash": resp.View.Hash}, nil

	case "update_modal":
		if err := requireArgs(op, args, "view_id", "view"); err != nil {
			return nil, err
		}
		view, err := parseModalView(args)
		if err != nil {
			return nil, err
		}
		resp, err := api.UpdateViewContext(ctx, view, "", argString(args, "view_hash"), argString(args, "view_id"))
		if err != nil {
			return nil, fmt.Errorf("slack update_modal: %w", err)
		}
		return map[string]any{"view_id": resp.View.ID}, nil

	case "publish_home":
		if err := requireArgs(op, args, "user_id", "view"); err != nil {
			return nil, err
		}
		var view slackgo.HomeTabViewRequest
		if err := unmarshalView(args, &view); err != nil {
			return nil, err
		}
		resp, err := api.PublishViewContext(ctx, slackgo.PublishViewContextRequest{
			UserID: argString(args, "user_id"),
			View:   view,
		})
		if err != nil {
			return nil, fmt.Errorf("slack publish_home: %w", err)
		}
		return map[string]any{"view_id": resp.View.ID}, nil

	case "respond_url":
		if err := requireArgs(op, args, "response_url"); err != nil {
			return nil, err
		}
		msg := &slackgo.WebhookMessage{
			Text:            argString(args, "text"),
			ResponseType:    argString(args, "response_type"),
			ReplaceOriginal: argBool(args, "replace_original"),
			DeleteOriginal:  argBool(args, "delete_original"),
		}
		if err := slackgo.PostWebhookContext(ctx, argString(args, "response_url"), msg); err != nil {
			return nil, fmt.Errorf("slack respond_url: %w", err)
		}
		return map[string]any{"ok": true}, nil

	default:
		return nil, fmt.Errorf("slack: unknown op %q", op)
	}
}

// openDM resolves a user to a DM channel ID via conversations.open.
func openDM(ctx context.Context, api slackWorkflowAPI, user string) (string, error) {
	ch, _, _, err := api.OpenConversationContext(ctx, &slackgo.OpenConversationParameters{Users: []string{user}})
	if err != nil {
		return "", fmt.Errorf("slack open_dm: %w", err)
	}
	return ch.ID, nil
}

// parseModalView decodes the `view` arg into a ModalViewRequest.
func parseModalView(args map[string]any) (slackgo.ModalViewRequest, error) {
	var v slackgo.ModalViewRequest
	err := unmarshalView(args, &v)
	return v, err
}

func unmarshalView(args map[string]any, dst any) error {
	raw := argString(args, "view")
	if raw == "" {
		return fmt.Errorf("slack: view is required")
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return fmt.Errorf("slack: view is not valid JSON: %w", err)
	}
	return nil
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func argBool(args map[string]any, key string) bool {
	b, _ := args[key].(bool)
	return b
}

func requireArgs(op string, args map[string]any, keys ...string) error {
	for _, k := range keys {
		if argString(args, k) == "" {
			return fmt.Errorf("slack %s: missing required arg %q", op, k)
		}
	}
	return nil
}
