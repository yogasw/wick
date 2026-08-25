package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// registerEventThreadStarted declares slack.thread_started — a top-level
// post that STARTS a thread, as opposed to a reply inside an existing one.
// Slack has no distinct "thread started" webhook; the channel adapter
// fires this alongside slack.message only when the message has no parent
// thread_ts (or thread_ts == its own ts). Payload + filter schema mirror
// slack.message so downstream nodes and the whitelist filter behave
// identically — the ONLY difference is replies never reach this event.
func registerEventThreadStarted(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "thread_started",
		Name:        "Slack: New thread",
		Description: "Fires only when a user starts a NEW thread (a top-level message), not on replies inside an existing thread. Bot's own messages are excluded.",
		PayloadType: MessageEvent{},
		MatchSchema: entity.StructToConfigs(SlackMessageMatch{}),
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.text":       "Raw message text that opened the thread.",
				"payload.user":       "Slack user ID (U…) of the thread starter.",
				"payload.channel_id": "Channel ID (C…) or DM ID (D…). Check payload.is_dm for the boolean.",
				"payload.thread":     "Thread root ts — always equal to payload.ts here (this message IS the thread root).",
				"payload.ts":         "Message timestamp / Slack message ID. Pass to reply_thread as the thread to answer in.",
				"payload.is_dm":      "True when channel_id starts with D (direct message).",
			},
			Quirks: []string{
				"Fires ONLY for thread roots. A reply to an existing thread fires slack.message but NOT this event.",
				"payload.thread == payload.ts always holds for this event (the message is its own thread root).",
				"Shares the single Slack message source with slack.message — both events can fire for the same top-level post, so don't wire the same workflow to both unless you want it twice.",
				"Bot's own messages are filtered before reaching this event — no loops from send_message.",
				"Filter activation needs BOTH MatchEnabled:true AND a non-empty Match map. Either alone is a no-op.",
			},
			PairWith: []string{
				"channel:slack.reply_thread",
				"channel:slack.add_reaction",
			},
			CommonPitfalls: []string{
				"Don't use this to catch replies — it never fires on replies. Use slack.message and branch on payload.thread != payload.ts if you need reply handling.",
				"Don't use mode:whitelist with an empty channel_id list expecting \"any channel\" — the router treats empty whitelist as \"match nothing\".",
			},
			InputSample:  `{"mode":"whitelist","channel_id":[{"id":"C12345","name":"#support"}]}`,
			OutputSample: `{"type":"channel","channel":"slack","event":"thread_started","at":"2026-05-19T10:32:17Z","payload":{"user":"U02ABCDEF","text":"anyone seen the staging deploy fail?","channel_id":"C12345","thread":"1700001234.005600","ts":"1700001234.005600","is_dm":false}}`,
			Examples: []wickdocs.Example{
				{
					Name: "new_threads_in_support",
					Body: `- type: channel
  channel: slack
  event: thread_started
  entry_node: classify
  match_enabled: true
  match:
    mode: whitelist
    channel_id:
      - { id: C12345, name: "#support" }`,
				},
			},
		},
	})
}
