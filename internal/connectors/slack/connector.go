// Package slack wraps Slack's Web API as a wick connector. One instance
// = one Slack workspace (bot token). Operations cover the most common
// LLM-driven workflows: reading channel/thread history, listing users
// and channels, sending/editing/deleting messages, and managing
// reactions. Designed as a drop-in replacement for the bundled Slack
// MCP server.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, thin handlers
//   - service.go   — URL/body construction, input validation, response shaping
//   - repo.go      — outbound HTTP via http.NewRequestWithContext
package slack

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const Key = "slack"

const defaultBaseURL = "https://slack.com/api"

// Configs is the per-instance credential set. One row = one workspace.
// AuthMode picks which token field the runtime reads — only the matching
// secret is shown in the admin UI thanks to visible_when.
type Configs struct {
	AuthMode  string `wick:"dropdown=bot_token|user_token;default=bot_token;desc=Which Slack OAuth token type to use. Bot tokens (xoxb-) cover the standard surface; user tokens (xoxp-) act as a workspace member and are required for ops that need user identity."`
	BotToken  string `wick:"secret;visible_when=auth_mode:bot_token;desc=Bot User OAuth Token (xoxb-...). Scopes: channels:read, groups:read, im:read, mpim:read, channels:history, groups:history, im:history, mpim:history, users:read, users:read.email, chat:write, chat:write.public, reactions:write, reactions:read."`
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
	Channel             string `wick:"required;desc=Channel ID (C..., G..., D...) or #channel-name."`
	IncludeNumMembers   bool   `wick:"desc=Include member count. Default: false."`
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
	Channel    string `wick:"required;desc=Channel ID where the message lives."`
	MessageTS  string `wick:"required;desc=Timestamp of the target message."`
}

type SendMessageInput struct {
	Channel  string `wick:"required;desc=Channel ID, user ID (DM), or #channel-name."`
	Text     string `wick:"textarea;desc=Fallback / plain-text body. Required if Blocks is empty."`
	Blocks   string `wick:"textarea;desc=Optional Block Kit JSON array (string). When set, supersedes text rendering."`
	ThreadTS string `wick:"desc=Parent message ts to reply in a thread."`
	ReplyBroadcast bool `wick:"desc=When replying in a thread, also broadcast to the channel. Default: false."`
	UnfurlLinks    bool `wick:"desc=Enable link unfurling. Default: true."`
	Mrkdwn         bool `wick:"desc=Enable Slack markdown rendering. Default: true."`
}

type SendEphemeralInput struct {
	Channel string `wick:"required;desc=Channel ID where the ephemeral will appear."`
	User    string `wick:"required;desc=User ID who will see the ephemeral message."`
	Text    string `wick:"textarea;desc=Plain-text body. Required if Blocks is empty."`
	Blocks  string `wick:"textarea;desc=Optional Block Kit JSON array (string)."`
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


// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Slack",
		Description: "Read channels, threads, and users; send, edit, and delete messages; manage reactions on Slack via the Web API.",
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
		),
		connector.Op(
			"search_channels",
			"Search Channels by Name",
			"Find channels whose name contains the query (case-insensitive). Returns up to {limit} matches with id, name, is_private.",
			SearchChannelsInput{},
			searchChannels,
		),
		connector.Op(
			"get_channel_info",
			"Get Channel Info",
			"Return metadata for a single channel: id, name, is_private, is_archived, topic, purpose, creator, created.",
			GetChannelInfoInput{},
			getChannelInfo,
		),
		connector.Op(
			"get_channel_history",
			"Get Channel History",
			"Read recent messages in a channel. Returns ts, user, text, thread_ts, reply_count, reactions, and pagination cursor.",
			GetChannelHistoryInput{},
			getChannelHistory,
		),
		connector.Op(
			"get_thread_replies",
			"Get Thread Replies",
			"Read all replies under a parent message thread. Returns parent + replies (ts, user, text, reactions) and pagination cursor.",
			GetThreadRepliesInput{},
			getThreadReplies,
		),
		connector.Op(
			"list_users",
			"List Users",
			"List workspace members. Returns id, name, real_name, email, is_bot, is_admin, deleted, and pagination cursor.",
			ListUsersInput{},
			listUsers,
		),
		connector.Op(
			"get_user_info",
			"Get User Info",
			"Return profile for a single user id: id, name, real_name, email, is_bot, is_admin, deleted.",
			GetUserInfoInput{},
			getUserInfo,
		),
		connector.Op(
			"get_user_by_email",
			"Get User by Email",
			"Resolve a workspace user by their email address. Returns the same shape as get_user_info.",
			GetUserByEmailInput{},
			getUserByEmail,
		),
		connector.Op(
			"get_permalink",
			"Get Message Permalink",
			"Return the permalink URL for a message ts in a channel.",
			GetPermalinkInput{},
			getPermalink,
		),
		connector.OpDestructive(
			"send_message",
			"Send Message",
			"Post a message to a channel, DM, or thread. Returns the posted message ts and channel id. Idempotent only if the caller dedupes upstream.",
			SendMessageInput{},
			sendMessage,
		),
		connector.OpDestructive(
			"send_ephemeral",
			"Send Ephemeral Message",
			"Post a message visible only to {user} in {channel}. Returns the message ts.",
			SendEphemeralInput{},
			sendEphemeral,
		),
		connector.OpDestructive(
			"update_message",
			"Update Message",
			"Edit a previously-sent message identified by ts. Returns the new ts and text.",
			UpdateMessageInput{},
			updateMessage,
		),
		connector.OpDestructive(
			"delete_message",
			"Delete Message",
			"Permanently delete a message by ts. Not reversible.",
			DeleteMessageInput{},
			deleteMessage,
		),
		connector.OpDestructive(
			"add_reaction",
			"Add Reaction",
			"Add an emoji reaction to a message. Emoji name is unprefixed (e.g. 'thumbsup').",
			AddReactionInput{},
			addReaction,
		),
		connector.OpDestructive(
			"remove_reaction",
			"Remove Reaction",
			"Remove an emoji reaction previously added by the bot.",
			RemoveReactionInput{},
			removeReaction,
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
	blocks := strings.TrimSpace(c.Input("blocks"))
	if strings.TrimSpace(text) == "" && blocks == "" {
		return nil, fmt.Errorf("text or blocks is required")
	}
	body := map[string]any{"channel": ch}
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
