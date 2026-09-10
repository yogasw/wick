package workflow

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// SendToSessionInput is the schema for slack.send_to_session. The thread
// addressed by (channel, thread_ts) IS the session: the same key a human
// mention would produce, so a workflow-started turn and a human follow-up
// share one conversation.
type SendToSessionInput struct {
	Channel   string `json:"channel"    wick:"required;desc=Channel ID holding the thread (C…)"`
	ThreadTS  string `json:"thread_ts"  wick:"required;key=thread_ts;desc=Parent message ts of the thread to run the agent in (root ts, not a reply ts)"`
	Text      string `json:"text"       wick:"required;textarea;desc=The message to hand the agent — written as an instruction, exactly as a person would type it in the thread"`
	AutoReply bool   `json:"auto_reply" wick:"key=auto_reply;desc=Also switch ON 🤖 auto-reply for this thread so later human replies continue the same session without a mention"`
}

// SendToSessionOutput reports where the work went, not what the agent said —
// the answer arrives asynchronously in the Slack thread, not in this node.
type SendToSessionOutput struct {
	SessionID  string `json:"session_id"`
	Queued     bool   `json:"queued"`
	NewSession bool   `json:"new_session"`
	AutoReply  string `json:"auto_reply,omitempty"`
}

func registerActionSendToSession(reg *integration.Registry, pick ChannelPicker) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "send_to_session",
		Name:        "Slack: Run agent in thread",
		Description: "Hand work to the agent session bound to a Slack thread, as if a person had posted the text there. The turn behaves like a mention: banner, streaming progress, reply in the thread. Returns as soon as the pool accepts it — the answer is NOT available to later nodes.",
		InputType:   SendToSessionInput{},
		OutputType:  SendToSessionOutput{},
		Destructive: true,
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"session_id":  "Session key the message was queued to (slack-<owner>-<thread_ts>). Same key a human mention in that thread produces.",
				"queued":      "True when the pool accepted the message. It does NOT mean the agent has finished.",
				"new_session": "True when this created the session; false when it continued an existing thread session.",
				"auto_reply":  "Only when auto_reply was requested: \"armed\", or \"skipped: <reason>\".",
			},
			TemplateableFields: []string{"channel", "thread_ts", "text"},
			Quirks: []string{
				"Fire-and-forget: the node returns when the message is QUEUED, so a downstream node cannot read the agent's answer. Put nothing after it that depends on the result.",
				"Prefer this over an `agent` node when the work is long or needs tools: an agent node runs headless (no MCP, no visible thinking), this runs a normal pool turn with the full toolset and streams progress into the thread.",
				"thread_ts must be the PARENT ts of the thread — usually the ts returned by a preceding send_message node, so the agent works in the thread of the card the workflow just posted.",
				"The session runs as the wick user who OWNS this Slack instance, not as whoever triggered the workflow. An instance with no owner errors instead of falling back to admin access.",
				"text must carry everything the agent needs (links, ids, which skill to use) — it starts with no view of the workflow's other nodes.",
				"Re-running the workflow on the same thread continues the SAME session rather than starting a fresh one.",
			},
			PairWith: []string{"channel:slack.send_message", "channel:slack.add_reaction"},
			InputSample: `{"channel":"C0BD4UHBQRJ","thread_ts":"1789014375.645429",` +
				`"text":"Run the support-ops-first-check skill on this inquiry, then continue into debug if it warrants it.","auto_reply":true}`,
			OutputSample: `{"session_id":"slack-u_123-1789014375.645429","queued":true,"new_session":true,"auto_reply":"armed"}`,
			Examples: []wickdocs.Example{
				{
					Name: "card_then_agent",
					Body: `- id: card
  type: channel
  channel: slack
  action: send_message
  args:
    channel: C0BD4UHBQRJ
    text: 'New inquiry — analysing…'
- id: run_agent
  type: channel
  channel: slack
  action: send_to_session
  arg_modes:
    thread_ts: expression
    text: expression
  args:
    channel: C0BD4UHBQRJ
    thread_ts: '{{.Node.card.ts}}'
    text: '{{.Node.prep.stdout}}'
    auto_reply: true`,
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			ch := pick(ctx)
			if ch == nil {
				return nil, fmt.Errorf("slack channel not configured")
			}
			channelID, err := argString(args, "channel")
			if err != nil {
				return nil, err
			}
			threadTS, err := argString(args, "thread_ts")
			if err != nil {
				return nil, err
			}
			text, err := argString(args, "text")
			if err != nil {
				return nil, err
			}
			autoReply := argBool(args, "auto_reply", false)
			sessionID, isNew, err := ch.SendToThreadSession(ctx, channelID, threadTS, text, autoReply)
			if err != nil {
				return nil, err
			}
			out := map[string]any{
				"session_id":  sessionID,
				"queued":      true,
				"new_session": isNew,
			}
			if autoReply {
				out["auto_reply"] = "armed"
			}
			return out, nil
		},
	})
}
