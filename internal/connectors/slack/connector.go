// Package slack wraps Slack's Web API as a wick connector. One instance
// = one Slack workspace (bot token). Operations cover the most common
// LLM-driven workflows: reading channel/thread history, listing users
// and channels, sending/editing/deleting messages, managing reactions,
// and creating or maintaining canvases. Designed as a drop-in replacement
// for the bundled Slack MCP server.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, thin handlers
//   - service.go   — URL/body construction, input validation, response shaping
//   - repo.go      — outbound HTTP via http.NewRequestWithContext
package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "slack"

const defaultBaseURL = "https://slack.com/api"

// Configs is the per-instance credential set. One row = one workspace.
// AuthMode picks which token field the runtime reads — only the matching
// secret is shown in the admin UI thanks to visible_when.
type Configs struct {
	AuthMode  string `wick:"dropdown=bot_token|user_token;default=bot_token;desc=Which Slack OAuth token type to use. Bot tokens (xoxb-) cover the standard surface; user tokens (xoxp-) act as a workspace member and are required for ops that need user identity."`
	BotToken  string `wick:"secret;visible_when=auth_mode:bot_token;desc=Bot User OAuth Token (xoxb-...). Scopes: channels:read, groups:read, im:read, mpim:read, channels:history, groups:history, im:history, mpim:history, users:read, users:read.email, chat:write, chat:write.public, reactions:write, reactions:read, canvases:read, canvases:write."`
	UserToken string `wick:"secret;visible_when=auth_mode:user_token;desc=User OAuth Token (xoxp-...). Filled automatically via the Connect with Slack button when client_id is set in Agents → Channels → Slack settings. Or paste manually."`
}

// ── Input structs ────────────────────────────────────────────────────

type ListChannelsInput struct {
	Types           string `wick:"desc=Comma-separated channel types: public_channel,private_channel,mpim,im. Default: public_channel,private_channel."`
	ExcludeArchived bool   `wick:"desc=Exclude archived channels. Default: true."`
	NameContains    string `wick:"desc=Optional case-insensitive substring filter on channel name (client-side)."`
	Limit           int    `wick:"desc=Max channels to return (1-1000). Default: 200."`
	Cursor          string `wick:"desc=Pagination cursor from a previous response."`
}

type SearchChannelsInput struct {
	Query           string `wick:"required;desc=Case-insensitive substring to match against channel names."`
	Types           string `wick:"desc=Comma-separated channel types. Default: public_channel,private_channel."`
	ExcludeArchived bool   `wick:"desc=Exclude archived channels. Default: true."`
	Limit           int    `wick:"desc=Max matches to return. Default: 50."`
}

type GetChannelInfoInput struct {
	Channel           string `wick:"required;desc=Channel ID (C..., G..., D...) or #channel-name."`
	IncludeNumMembers bool   `wick:"desc=Include member count. Default: false."`
}

type GetChannelHistoryInput struct {
	Channel string `wick:"required;desc=Channel ID. Use list_channels to resolve names to IDs."`
	Limit   int    `wick:"desc=Max messages (1-1000). Default: 50."`
	Cursor  string `wick:"desc=Pagination cursor from previous response."`
	Oldest  string `wick:"desc=Inclusive start timestamp (Slack ts, e.g. 1700000000.000100)."`
	Latest  string `wick:"desc=Inclusive end timestamp."`
}

type GetThreadRepliesInput struct {
	Channel  string `wick:"required;desc=Channel ID containing the thread."`
	ThreadTS string `wick:"required;desc=Timestamp of the thread's parent message (e.g. 1700000000.000100)."`
	Limit    int    `wick:"desc=Max replies (1-1000). Default: 100."`
	Cursor   string `wick:"desc=Pagination cursor."`
}

type ListUsersInput struct {
	Limit          int    `wick:"desc=Max users per page (1-1000). Default: 200."`
	Cursor         string `wick:"desc=Pagination cursor."`
	IncludeDeleted bool   `wick:"desc=Include deactivated users. Default: false."`
}

type GetUserInfoInput struct {
	User string `wick:"required;desc=User ID (U... or W...)."`
}

type GetUserByEmailInput struct {
	Email string `wick:"required;email;desc=Email address registered to a workspace user."`
}

type GetPermalinkInput struct {
	Channel   string `wick:"required;desc=Channel ID where the message lives."`
	MessageTS string `wick:"required;desc=Timestamp of the target message."`
}

type SendMessageInput struct {
	Channel        string `wick:"required;desc=Channel ID, user ID (DM), or #channel-name."`
	Text           string `wick:"textarea;desc=Fallback / plain-text body. Required if Blocks is empty."`
	Blocks         string `wick:"textarea;desc=Optional Block Kit JSON array (string). When set, supersedes text rendering."`
	ThreadTS       string `wick:"desc=Parent message ts to reply in a thread."`
	ReplyBroadcast bool   `wick:"desc=When replying in a thread, also broadcast to the channel. Default: false."`
	UnfurlLinks    bool   `wick:"desc=Enable link unfurling. Default: true."`
	Mrkdwn         bool   `wick:"desc=Enable Slack markdown rendering. Default: true."`
}

type SendEphemeralInput struct {
	Channel  string `wick:"required;desc=Channel ID where the ephemeral will appear."`
	User     string `wick:"required;desc=User ID who will see the ephemeral message."`
	Text     string `wick:"textarea;desc=Plain-text body. Required if Blocks is empty."`
	Blocks   string `wick:"textarea;desc=Optional Block Kit JSON array (string)."`
	ThreadTS string `wick:"desc=Optional parent thread ts."`
}

type UpdateMessageInput struct {
	Channel string `wick:"required;desc=Channel ID containing the message."`
	TS      string `wick:"required;desc=Timestamp of the message to edit."`
	Text    string `wick:"textarea;desc=New plain-text body."`
	Blocks  string `wick:"textarea;desc=New Block Kit JSON array (string)."`
}

type DeleteMessageInput struct {
	Channel string `wick:"required;desc=Channel ID containing the message."`
	TS      string `wick:"required;desc=Timestamp of the message to delete."`
}

type AddReactionInput struct {
	Channel string `wick:"required;desc=Channel ID containing the message."`
	TS      string `wick:"required;desc=Timestamp of the target message."`
	Name    string `wick:"required;desc=Emoji name without colons. Example: thumbsup, white_check_mark."`
}

type RemoveReactionInput struct {
	Channel string `wick:"required;desc=Channel ID containing the message."`
	TS      string `wick:"required;desc=Timestamp of the target message."`
	Name    string `wick:"required;desc=Emoji name without colons."`
}

type CreateCanvasInput struct {
	Title     string `wick:"desc=Optional title of the standalone canvas."`
	Markdown  string `wick:"textarea;desc=Optional initial canvas body in Slack-supported markdown."`
	ChannelID string `wick:"desc=Optional channel ID to add the standalone canvas as a channel tab with write access."`
}

type CreateChannelCanvasInput struct {
	ChannelID string `wick:"required;desc=Channel ID that will own the channel canvas."`
	Title     string `wick:"desc=Optional title of the channel canvas."`
	Markdown  string `wick:"textarea;desc=Optional initial canvas body in Slack-supported markdown."`
}

type EditCanvasInput struct {
	CanvasID  string `wick:"required;desc=Canvas ID (F...) to edit."`
	Operation string `wick:"dropdown=insert_at_end|insert_at_start|insert_after|insert_before|replace|delete|rename;default=insert_at_end;desc=Edit action. Section ID is required for insert_before, insert_after, and delete."`
	SectionID string `wick:"desc=Section ID returned by lookup_canvas_sections. Optional for replace; required for relative insertion and delete."`
	Markdown  string `wick:"textarea;desc=Markdown content for insert or replace, or new title for rename. Not used for delete."`
}

type LookupCanvasSectionsInput struct {
	CanvasID string `wick:"required;desc=Canvas ID (F...) to inspect."`
	Criteria string `wick:"required;textarea;desc=JSON criteria object, for example {\"section_types\":[\"any_header\"],\"contains_text\":\"Incident\"}."`
}

type SetCanvasAccessInput struct {
	CanvasID    string `wick:"required;desc=Standalone canvas ID (F...) to share."`
	AccessLevel string `wick:"dropdown=read|write|owner;default=read;desc=Access to grant. owner is valid only for user_ids."`
	ChannelIDs  string `wick:"desc=Comma-separated channel IDs to grant access. Cannot be combined with user_ids."`
	UserIDs     string `wick:"desc=Comma-separated user IDs to grant access. Cannot be combined with channel_ids."`
}

// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Slack",
		Description: "Read channels, threads, and users; send, edit, and delete messages; manage reactions and canvases on Slack via the Web API.",
		Icon:        "💬",
	}
}

// Operations returns the LLM-callable actions for this connector.
func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"list_channels",
			"List Channels",
			"List channels visible to the bot. Returns id, name, is_private, is_archived, topic, purpose, and pagination cursor.",
			ListChannelsInput{},
			listChannels,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"channels":          "Array of channel objects: id, name, is_private, is_archived, topic, purpose, num_members.",
					"response_metadata": "Pagination wrapper. response_metadata.next_cursor non-empty = call again with cursor.",
				},
				Quirks: []string{
					"types defaults to public_channel,private_channel. Add mpim,im to include group DMs and DMs.",
					"exclude_archived defaults true — flip to false when auditing/restoring.",
					"name_contains is applied client-side after Slack returns the page; if your match is in a later page it won't surface without paginating.",
					"Slack caps the per-call result at 1000. Use cursor for the next page.",
				},
				PairWith:     []string{"connector:slack.search_channels", "connector:slack.get_channel_info"},
				OutputSample: `{"ok":true,"channels":[{"id":"C12345","name":"general","is_private":false,"is_archived":false}],"response_metadata":{"next_cursor":""}}`,
			},
		),
		connector.Op(
			"search_channels",
			"Search Channels by Name",
			"Find channels whose name contains the query (case-insensitive). Returns up to {limit} matches with id, name, is_private.",
			SearchChannelsInput{},
			searchChannels, wickdocs.Docs{},
		),
		connector.Op(
			"get_channel_info",
			"Get Channel Info",
			"Return metadata for a single channel: id, name, is_private, is_archived, topic, purpose, creator, created.",
			GetChannelInfoInput{},
			getChannelInfo, wickdocs.Docs{},
		),
		connector.Op(
			"get_channel_history",
			"Get Channel History",
			"Read recent messages in a channel. Returns ts, user, text, thread_ts, reply_count, reactions, and pagination cursor.",
			GetChannelHistoryInput{},
			getChannelHistory,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"messages":          "Array of message objects (ts, user, text, thread_ts, reply_count, reactions).",
					"response_metadata": "Pagination wrapper. response_metadata.next_cursor non-empty = more pages.",
					"has_more":          "True when older messages exist beyond the current page.",
				},
				Quirks: []string{
					"oldest / latest are INCLUSIVE Slack ts strings (e.g. \"1700000000.000100\"), not RFC3339.",
					"Default limit 50, max 1000. For long ranges paginate with cursor instead of bumping limit.",
					"Returns ONLY top-level messages — threaded replies need get_thread_replies on the parent ts.",
					"Requires channels:history (public), groups:history (private), or im:history / mpim:history depending on channel type.",
				},
				PairWith:     []string{"connector:slack.get_thread_replies", "connector:slack.get_permalink"},
				InputSample:  `{"channel":"C12345","limit":50,"oldest":"1700000000.000000"}`,
				OutputSample: `{"ok":true,"messages":[{"ts":"1700001234.005600","user":"U02ABCDEF","text":"hello","thread_ts":"1700001234.005600","reply_count":3}],"has_more":false,"response_metadata":{"next_cursor":""}}`,
			},
		),
		connector.Op(
			"get_thread_replies",
			"Get Thread Replies",
			"Read all replies under a parent message thread. Returns parent + replies (ts, user, text, reactions) and pagination cursor.",
			GetThreadRepliesInput{},
			getThreadReplies, wickdocs.Docs{},
		),
		connector.Op(
			"list_users",
			"List Users",
			"List workspace members. Returns id, name, real_name, email, is_bot, is_admin, deleted, and pagination cursor.",
			ListUsersInput{},
			listUsers,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"members":           "Array of user objects (id, name, real_name, profile.email, is_bot, is_admin, deleted).",
					"response_metadata": "Pagination wrapper — response_metadata.next_cursor.",
				},
				Quirks: []string{
					"Slack returns deactivated users by default — set include_deleted=false-ish via custom filter in your workflow if you don't want them.",
					"Email visibility requires users:read.email scope. Without it the email field is empty.",
					"Tier-2 rate limit — paginate, don't tight-loop.",
				},
				PairWith:     []string{"connector:slack.get_user_info", "connector:slack.get_user_by_email"},
				OutputSample: `{"ok":true,"members":[{"id":"U02ABCDEF","name":"yoga","real_name":"Yoga Setiawan","profile":{"email":"yoga@example.com"},"is_bot":false,"deleted":false}],"response_metadata":{"next_cursor":""}}`,
			},
		),
		connector.Op(
			"get_user_info",
			"Get User Info",
			"Return profile for a single user id: id, name, real_name, email, is_bot, is_admin, deleted.",
			GetUserInfoInput{},
			getUserInfo, wickdocs.Docs{},
		),
		connector.Op(
			"get_user_by_email",
			"Get User by Email",
			"Resolve a workspace user by their email address. Returns the same shape as get_user_info.",
			GetUserByEmailInput{},
			getUserByEmail,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"user": "Single user object (id, name, real_name, profile.email, is_bot, is_admin, deleted). Empty when no match.",
				},
				Quirks: []string{
					"Requires users:read.email scope.",
					"Slack returns users_not_found when no workspace member uses that exact email — wick surfaces the error verbatim.",
					"Email is matched case-insensitively but must be the FULL address (no aliasing).",
				},
				PairWith:     []string{"connector:slack.get_user_info", "channel:slack.open_dm"},
				InputSample:  `{"email":"yoga@example.com"}`,
				OutputSample: `{"ok":true,"user":{"id":"U02ABCDEF","name":"yoga","real_name":"Yoga Setiawan","profile":{"email":"yoga@example.com"},"is_bot":false}}`,
			},
		),
		connector.Op(
			"get_permalink",
			"Get Message Permalink",
			"Return the permalink URL for a message ts in a channel.",
			GetPermalinkInput{},
			getPermalink, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"send_message",
			"Send Message",
			"Post a message to a channel, DM, or thread. Returns the posted message ts and channel id. Idempotent only if the caller dedupes upstream.",
			SendMessageInput{},
			sendMessage,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"ok":      "Slack API success flag. False means the request reached Slack but the post was rejected.",
					"channel": "Channel ID the message landed in (C…/D…/G…). Resolved from #name input server-side.",
					"ts":      "Slack message timestamp / message ID. Pass to update_message, delete_message, get_permalink, add_reaction, or as thread_ts for follow-ups.",
					"message": "Echoed payload Slack accepted, including normalised text/blocks. Useful for audit fixtures.",
				},
				TemplateableFields: []string{"channel", "text", "blocks", "thread_ts"},
				Quirks: []string{
					"channel accepts: channel ID (C…), DM ID (D…), user ID (U…) for an auto-opened DM, or #channel-name (resolved server-side). #name fails when the bot isn't already a member.",
					"When blocks is set it OVERRIDES text for rendering, but Slack still requires a non-empty text for notifications. Always set both.",
					"thread_ts must be the PARENT message's ts. Replying to a reply still uses the root ts.",
					"reply_broadcast only does anything when thread_ts is set. With it true, the threaded reply also fans out to the main channel.",
					"Rate limit: 1 msg/sec per channel for posting. Bursts beyond that get queued then 429'd.",
				},
				PairWith: []string{
					"connector:slack.update_message",
					"connector:slack.delete_message",
					"connector:slack.add_reaction",
					"channel:slack.send_message",
				},
				CommonPitfalls: []string{
					"Don't put both text and blocks as templates and forget to {{jsonEscape}} text inside blocks — unescaped quotes break the JSON.",
					"Don't pass channel: \"#name\" expecting Slack to auto-invite the bot — name resolution succeeds only when the bot is already a member.",
					"Don't reuse a stored ts across days for thread_ts on a high-volume channel — Slack purges old thread state.",
				},
				InputSample:  `{"channel":"#alerts","text":"New ticket from U12345: payment refund issue","thread_ts":"1700000000.000100"}`,
				OutputSample: `{"ok":true,"channel":"C12345","ts":"1700001234.005600","message":{"text":"New ticket from U12345: payment refund issue","user":"UBOT01","ts":"1700001234.005600"}}`,
				Examples: []wickdocs.Example{
					{
						Name: "simple_send",
						Body: `- id: notify
  type: connector
  module: slack
  op: send_message
  arg_modes:
    text: expression
  args:
    channel: "#alerts"
    text: "New ticket from {{.Node.trigger.payload.user}}: {{.Node.trigger.payload.text}}"`,
					},
					{
						Name: "thread_reply",
						Body: `- id: reply
  type: connector
  module: slack
  op: send_message
  arg_modes:
    channel: expression
    text: expression
    thread_ts: expression
  args:
    channel: '{{.Node.trigger.payload.channel_id}}'
    thread_ts: '{{.Node.trigger.payload.thread}}'
    text: "Got it — looking into this now."`,
					},
					{
						Name: "with_blocks",
						Body: `- id: alert
  type: connector
  module: slack
  op: send_message
  args:
    channel: "#alerts"
    text: "Service down — payments-api"
    blocks: |
      [
        {"type":"header","text":{"type":"plain_text","text":":rotating_light: Service down"}},
        {"type":"section","fields":[
          {"type":"mrkdwn","text":"*Service:*\npayments-api"},
          {"type":"mrkdwn","text":"*Region:*\nap-southeast-1"}
        ]},
        {"type":"actions","elements":[
          {"type":"button","text":{"type":"plain_text","text":"Open runbook"},"url":"https://example.com/runbook"}
        ]}
      ]`,
					},
				},
			},
		),
		connector.OpDestructive(
			"send_ephemeral",
			"Send Ephemeral Message",
			"Post a message visible only to {user} in {channel}. Returns the message ts.",
			SendEphemeralInput{},
			sendEphemeral, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"update_message",
			"Update Message",
			"Edit a previously-sent message identified by ts. Returns the new ts and text.",
			UpdateMessageInput{},
			updateMessage,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"ok":      "True on success.",
					"channel": "Channel ID (echo).",
					"ts":      "Edited message ts (matches input).",
					"text":    "Echoed new text.",
				},
				TemplateableFields: []string{"channel", "ts", "text", "blocks"},
				Quirks: []string{
					"Can only edit messages the bot itself posted. Other authors' messages return cant_update_message.",
					"Doesn't work on ephemerals — use respond_url with replace_original.",
					"blocks REPLACES the entire block set. Pass full new state, not a diff.",
				},
				PairWith:     []string{"connector:slack.send_message", "connector:slack.delete_message"},
				InputSample:  `{"channel":"C12345","ts":"1700001234.005600","text":"Resolved. Thanks!"}`,
				OutputSample: `{"ok":true,"channel":"C12345","ts":"1700001234.005600","text":"Resolved. Thanks!"}`,
			},
		),
		connector.OpDestructive(
			"delete_message",
			"Delete Message",
			"Permanently delete a message by ts. Not reversible.",
			DeleteMessageInput{},
			deleteMessage, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"add_reaction",
			"Add Reaction",
			"Add an emoji reaction to a message. Emoji name is unprefixed (e.g. 'thumbsup').",
			AddReactionInput{},
			addReaction,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"ok": "True on success or already_reacted.",
				},
				TemplateableFields: []string{"channel", "ts", "name"},
				Quirks: []string{
					"name is the emoji shortname WITHOUT colons (\"thumbsup\", not \":thumbsup:\").",
					"Idempotent: re-adding the same reaction errors with already_reacted — wrap with on_failure: skip if you don't want to fail the workflow.",
					"Requires reactions:write scope.",
				},
				PairWith:     []string{"connector:slack.remove_reaction"},
				InputSample:  `{"channel":"C12345","ts":"1700001234.005600","name":"white_check_mark"}`,
				OutputSample: `{"ok":true}`,
			},
		),
		connector.OpDestructive(
			"remove_reaction",
			"Remove Reaction",
			"Remove an emoji reaction previously added by the bot.",
			RemoveReactionInput{},
			removeReaction, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"create_canvas",
			"Create Canvas",
			"Create a standalone Slack canvas, optionally pre-filled with markdown and added as a channel tab.",
			CreateCanvasInput{},
			createCanvas,
			wickdocs.Docs{
				TemplateableFields: []string{"title", "markdown", "channel_id"},
				Quirks: []string{
					"Requires canvases:write scope.",
					"On free Slack teams, channel_id is required because non-tabbed standalone canvases cannot be created.",
					"Use set_canvas_access to share a standalone canvas after creation.",
				},
				PairWith: []string{"connector:slack.set_canvas_access", "connector:slack.send_message"},
			},
		),
		connector.OpDestructive(
			"create_channel_canvas",
			"Create Channel Canvas",
			"Create the dedicated canvas for a channel, optionally pre-filled with markdown.",
			CreateChannelCanvasInput{},
			createChannelCanvas,
			wickdocs.Docs{
				TemplateableFields: []string{"channel_id", "title", "markdown"},
				Quirks: []string{
					"Each channel can have only one channel canvas; Slack returns channel_canvas_already_exists if one is already present.",
					"Access follows channel membership, so set_canvas_access is unnecessary for this operation.",
				},
			},
		),
		connector.OpDestructive(
			"edit_canvas",
			"Edit Canvas",
			"Append, insert, replace, delete a section, or rename an existing Slack canvas.",
			EditCanvasInput{},
			editCanvas,
			wickdocs.Docs{
				TemplateableFields: []string{"canvas_id", "operation", "section_id", "markdown"},
				PairWith:           []string{"connector:slack.lookup_canvas_sections"},
			},
		),
		connector.Op(
			"lookup_canvas_sections",
			"Lookup Canvas Sections",
			"Find canvas section IDs by heading type or contained text for subsequent targeted edits.",
			LookupCanvasSectionsInput{},
			lookupCanvasSections,
			wickdocs.Docs{
				PairWith: []string{"connector:slack.edit_canvas"},
			},
		),
		connector.OpDestructive(
			"set_canvas_access",
			"Set Canvas Access",
			"Grant read, write, or owner access to a standalone canvas for channels or users.",
			SetCanvasAccessInput{},
			setCanvasAccess,
			wickdocs.Docs{
				TemplateableFields: []string{"canvas_id", "access_level", "channel_ids", "user_ids"},
				Quirks: []string{
					"Provide channel_ids or user_ids, never both.",
					"Only user_ids may receive owner access.",
					"Slack requires the canvas link to have been shared with each target channel or user before setting its access.",
				},
			},
		),
	}
}

// HealthCheck verifies the configured token and reports per-operation
// permission status. Surfaced in the framework: the connector detail
// page renders a "Check Permissions" button when this is non-nil, and
// failing ops get system-disabled with a reason like
// "needs scope: chat:write".
func HealthCheck(c *connector.Ctx) ([]connector.OpHealth, error) {
	return runHealthCheck(c)
}

// ── Operation handlers ───────────────────────────────────────────────

func listChannels(c *connector.Ctx) (any, error) {
	form := map[string]string{
		"types":            firstNonEmpty(c.Input("types"), "public_channel,private_channel"),
		"exclude_archived": boolForm(c.InputBool("exclude_archived"), true),
		"limit":            fmt.Sprintf("%d", clampInt(c.InputInt("limit"), 1, 1000, 200)),
	}
	if cursor := strings.TrimSpace(c.Input("cursor")); cursor != "" {
		form["cursor"] = cursor
	}
	raw, err := slackGet(c, "conversations.list", form)
	if err != nil {
		return nil, err
	}
	return shapeChannelList(raw, strings.ToLower(strings.TrimSpace(c.Input("name_contains")))), nil
}

func searchChannels(c *connector.Ctx) (any, error) {
	q := strings.TrimSpace(c.Input("query"))
	if q == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := clampInt(c.InputInt("limit"), 1, 1000, 50)
	form := map[string]string{
		"types":            firstNonEmpty(c.Input("types"), "public_channel,private_channel"),
		"exclude_archived": boolForm(c.InputBool("exclude_archived"), true),
		"limit":            "1000",
	}
	raw, err := slackGet(c, "conversations.list", form)
	if err != nil {
		return nil, err
	}
	return shapeChannelSearch(raw, strings.ToLower(q), limit), nil
}

func getChannelInfo(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	form := map[string]string{
		"channel":             ch,
		"include_num_members": boolForm(c.InputBool("include_num_members"), false),
	}
	raw, err := slackGet(c, "conversations.info", form)
	if err != nil {
		return nil, err
	}
	return shapeChannelInfo(raw), nil
}

func getChannelHistory(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	form := map[string]string{
		"channel": ch,
		"limit":   fmt.Sprintf("%d", clampInt(c.InputInt("limit"), 1, 1000, 50)),
	}
	if v := strings.TrimSpace(c.Input("cursor")); v != "" {
		form["cursor"] = v
	}
	if v := strings.TrimSpace(c.Input("oldest")); v != "" {
		form["oldest"] = v
		form["inclusive"] = "true"
	}
	if v := strings.TrimSpace(c.Input("latest")); v != "" {
		form["latest"] = v
		form["inclusive"] = "true"
	}
	raw, err := slackGet(c, "conversations.history", form)
	if err != nil {
		return nil, err
	}
	return shapeMessageList(raw), nil
}

func getThreadReplies(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	ts := strings.TrimSpace(c.Input("thread_ts"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if ts == "" {
		return nil, fmt.Errorf("thread_ts is required")
	}
	form := map[string]string{
		"channel": ch,
		"ts":      ts,
		"limit":   fmt.Sprintf("%d", clampInt(c.InputInt("limit"), 1, 1000, 100)),
	}
	if v := strings.TrimSpace(c.Input("cursor")); v != "" {
		form["cursor"] = v
	}
	raw, err := slackGet(c, "conversations.replies", form)
	if err != nil {
		return nil, err
	}
	return shapeMessageList(raw), nil
}

func listUsers(c *connector.Ctx) (any, error) {
	form := map[string]string{
		"limit": fmt.Sprintf("%d", clampInt(c.InputInt("limit"), 1, 1000, 200)),
	}
	if v := strings.TrimSpace(c.Input("cursor")); v != "" {
		form["cursor"] = v
	}
	raw, err := slackGet(c, "users.list", form)
	if err != nil {
		return nil, err
	}
	return shapeUserList(raw, c.InputBool("include_deleted")), nil
}

func getUserInfo(c *connector.Ctx) (any, error) {
	u := strings.TrimSpace(c.Input("user"))
	if u == "" {
		return nil, fmt.Errorf("user is required")
	}
	raw, err := slackGet(c, "users.info", map[string]string{"user": u})
	if err != nil {
		return nil, err
	}
	return shapeUserSingle(raw), nil
}

func getUserByEmail(c *connector.Ctx) (any, error) {
	email := strings.TrimSpace(c.Input("email"))
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	raw, err := slackGet(c, "users.lookupByEmail", map[string]string{"email": email})
	if err != nil {
		return nil, err
	}
	return shapeUserSingle(raw), nil
}

func getPermalink(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	ts := strings.TrimSpace(c.Input("message_ts"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if ts == "" {
		return nil, fmt.Errorf("message_ts is required")
	}
	raw, err := slackGet(c, "chat.getPermalink", map[string]string{
		"channel":    ch,
		"message_ts": ts,
	})
	if err != nil {
		return nil, err
	}
	if m, ok := raw.(map[string]any); ok {
		return map[string]any{
			"permalink": m["permalink"],
			"channel":   m["channel"],
		}, nil
	}
	return raw, nil
}

func sendMessage(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	text := c.Input("text")
	blocksRaw := strings.TrimSpace(c.Input("blocks"))
	if strings.TrimSpace(text) == "" && blocksRaw == "" {
		return nil, fmt.Errorf("text or blocks is required")
	}
	body := map[string]any{"channel": ch}
	if text != "" {
		// Keep text for notification preview / fallback even when we
		// render via blocks below.
		body["text"] = text
	}
	// Append the signed footer to every send_message. If the caller
	// supplied blocks, append onto theirs; otherwise wrap the plain
	// text in a section block first so the footer renders alongside.
	var outBlocks []any
	if blocksRaw != "" {
		parsed, err := parseBlocks(blocksRaw)
		if err != nil {
			return nil, err
		}
		if arr, ok := parsed.([]any); ok {
			outBlocks = arr
		}
	} else if text != "" {
		outBlocks = []any{
			map[string]any{
				"type": "section",
				"text": map[string]any{"type": "mrkdwn", "text": text},
			},
		}
	}
	outBlocks = append(outBlocks, signedFooterBlock(c))
	body["blocks"] = outBlocks
	if v := strings.TrimSpace(c.Input("thread_ts")); v != "" {
		body["thread_ts"] = v
		if c.InputBool("reply_broadcast") {
			body["reply_broadcast"] = true
		}
	}
	body["unfurl_links"] = c.InputBool("unfurl_links") || strings.TrimSpace(c.Input("unfurl_links")) == ""
	body["mrkdwn"] = c.InputBool("mrkdwn") || strings.TrimSpace(c.Input("mrkdwn")) == ""

	raw, err := slackPost(c, "chat.postMessage", body)
	if err != nil {
		return nil, err
	}
	return shapePostResult(raw), nil
}

func sendEphemeral(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	user := strings.TrimSpace(c.Input("user"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if user == "" {
		return nil, fmt.Errorf("user is required")
	}
	text := c.Input("text")
	blocks := strings.TrimSpace(c.Input("blocks"))
	if strings.TrimSpace(text) == "" && blocks == "" {
		return nil, fmt.Errorf("text or blocks is required")
	}
	body := map[string]any{"channel": ch, "user": user}
	if text != "" {
		body["text"] = text
	}
	if blocks != "" {
		parsed, err := parseBlocks(blocks)
		if err != nil {
			return nil, err
		}
		body["blocks"] = parsed
	}
	if v := strings.TrimSpace(c.Input("thread_ts")); v != "" {
		body["thread_ts"] = v
	}
	raw, err := slackPost(c, "chat.postEphemeral", body)
	if err != nil {
		return nil, err
	}
	if m, ok := raw.(map[string]any); ok {
		return map[string]any{"message_ts": m["message_ts"]}, nil
	}
	return raw, nil
}

func updateMessage(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	ts := strings.TrimSpace(c.Input("ts"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if ts == "" {
		return nil, fmt.Errorf("ts is required")
	}
	text := c.Input("text")
	blocks := strings.TrimSpace(c.Input("blocks"))
	if strings.TrimSpace(text) == "" && blocks == "" {
		return nil, fmt.Errorf("text or blocks is required")
	}
	body := map[string]any{"channel": ch, "ts": ts}
	if text != "" {
		body["text"] = text
	}
	if blocks != "" {
		parsed, err := parseBlocks(blocks)
		if err != nil {
			return nil, err
		}
		body["blocks"] = parsed
	}
	raw, err := slackPost(c, "chat.update", body)
	if err != nil {
		return nil, err
	}
	return shapePostResult(raw), nil
}

func deleteMessage(c *connector.Ctx) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	ts := strings.TrimSpace(c.Input("ts"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if ts == "" {
		return nil, fmt.Errorf("ts is required")
	}
	raw, err := slackPost(c, "chat.delete", map[string]any{"channel": ch, "ts": ts})
	if err != nil {
		return nil, err
	}
	if m, ok := raw.(map[string]any); ok {
		return map[string]any{"channel": m["channel"], "ts": m["ts"]}, nil
	}
	return raw, nil
}

func addReaction(c *connector.Ctx) (any, error) {
	return reactionAction(c, "reactions.add")
}

func removeReaction(c *connector.Ctx) (any, error) {
	return reactionAction(c, "reactions.remove")
}

func reactionAction(c *connector.Ctx, method string) (any, error) {
	ch := strings.TrimSpace(c.Input("channel"))
	ts := strings.TrimSpace(c.Input("ts"))
	name := strings.TrimSpace(c.Input("name"))
	if ch == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if ts == "" {
		return nil, fmt.Errorf("ts is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	name = strings.Trim(name, ":")
	_, err := slackPost(c, method, map[string]any{
		"channel":   ch,
		"timestamp": ts,
		"name":      name,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "channel": ch, "ts": ts, "name": name}, nil
}

func createCanvas(c *connector.Ctx) (any, error) {
	body := map[string]any{}
	if title := strings.TrimSpace(c.Input("title")); title != "" {
		body["title"] = title
	}
	if markdown := c.Input("markdown"); strings.TrimSpace(markdown) != "" {
		body["document_content"] = canvasDocument(markdown)
	}
	if channelID := strings.TrimSpace(c.Input("channel_id")); channelID != "" {
		body["channel_id"] = channelID
	}
	return slackPost(c, "canvases.create", body)
}

func createChannelCanvas(c *connector.Ctx) (any, error) {
	channelID := strings.TrimSpace(c.Input("channel_id"))
	if channelID == "" {
		return nil, fmt.Errorf("channel_id is required")
	}
	body := map[string]any{"channel_id": channelID}
	if title := strings.TrimSpace(c.Input("title")); title != "" {
		body["title"] = title
	}
	if markdown := c.Input("markdown"); strings.TrimSpace(markdown) != "" {
		body["document_content"] = canvasDocument(markdown)
	}
	return slackPost(c, "conversations.canvases.create", body)
}

func editCanvas(c *connector.Ctx) (any, error) {
	canvasID := strings.TrimSpace(c.Input("canvas_id"))
	if canvasID == "" {
		return nil, fmt.Errorf("canvas_id is required")
	}
	operation := firstNonEmpty(strings.TrimSpace(c.Input("operation")), "insert_at_end")
	sectionID := strings.TrimSpace(c.Input("section_id"))
	markdown := c.Input("markdown")
	change := map[string]any{"operation": operation}

	switch operation {
	case "insert_after", "insert_before":
		if sectionID == "" {
			return nil, fmt.Errorf("section_id is required for %s", operation)
		}
		if strings.TrimSpace(markdown) == "" {
			return nil, fmt.Errorf("markdown is required for %s", operation)
		}
		change["section_id"] = sectionID
		change["document_content"] = canvasDocument(markdown)
	case "insert_at_start", "insert_at_end":
		if strings.TrimSpace(markdown) == "" {
			return nil, fmt.Errorf("markdown is required for %s", operation)
		}
		change["document_content"] = canvasDocument(markdown)
	case "replace":
		if strings.TrimSpace(markdown) == "" {
			return nil, fmt.Errorf("markdown is required for replace")
		}
		if sectionID != "" {
			change["section_id"] = sectionID
		}
		change["document_content"] = canvasDocument(markdown)
	case "delete":
		if sectionID == "" {
			return nil, fmt.Errorf("section_id is required for delete")
		}
		change["section_id"] = sectionID
	case "rename":
		if strings.TrimSpace(markdown) == "" {
			return nil, fmt.Errorf("markdown is required for rename")
		}
		change["title_content"] = canvasDocument(markdown)
	default:
		return nil, fmt.Errorf("unsupported operation %q", operation)
	}

	return slackPost(c, "canvases.edit", map[string]any{
		"canvas_id": canvasID,
		"changes":   []map[string]any{change},
	})
}

func lookupCanvasSections(c *connector.Ctx) (any, error) {
	canvasID := strings.TrimSpace(c.Input("canvas_id"))
	if canvasID == "" {
		return nil, fmt.Errorf("canvas_id is required")
	}
	rawCriteria := strings.TrimSpace(c.Input("criteria"))
	if rawCriteria == "" {
		return nil, fmt.Errorf("criteria is required")
	}
	var criteria map[string]any
	if err := json.Unmarshal([]byte(rawCriteria), &criteria); err != nil {
		return nil, fmt.Errorf("criteria must be a JSON object: %w", err)
	}
	return slackPost(c, "canvases.sections.lookup", map[string]any{
		"canvas_id": canvasID,
		"criteria":  criteria,
	})
}

func setCanvasAccess(c *connector.Ctx) (any, error) {
	canvasID := strings.TrimSpace(c.Input("canvas_id"))
	if canvasID == "" {
		return nil, fmt.Errorf("canvas_id is required")
	}
	level := firstNonEmpty(strings.TrimSpace(c.Input("access_level")), "read")
	if level != "read" && level != "write" && level != "owner" {
		return nil, fmt.Errorf("access_level must be read, write, or owner")
	}
	channelIDs := parseCanvasIDs(c.Input("channel_ids"))
	userIDs := parseCanvasIDs(c.Input("user_ids"))
	if len(channelIDs) == 0 && len(userIDs) == 0 {
		return nil, fmt.Errorf("channel_ids or user_ids is required")
	}
	if len(channelIDs) > 0 && len(userIDs) > 0 {
		return nil, fmt.Errorf("channel_ids and user_ids cannot be combined")
	}
	if level == "owner" && len(channelIDs) > 0 {
		return nil, fmt.Errorf("owner access is valid only for user_ids")
	}
	body := map[string]any{"canvas_id": canvasID, "access_level": level}
	if len(channelIDs) > 0 {
		body["channel_ids"] = channelIDs
	} else {
		body["user_ids"] = userIDs
	}
	return slackPost(c, "canvases.access.set", body)
}

func canvasDocument(markdown string) map[string]any {
	return map[string]any{"type": "markdown", "markdown": markdown}
}

func parseCanvasIDs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, value := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(value); id != "" {
			out = append(out, id)
		}
	}
	return out
}
