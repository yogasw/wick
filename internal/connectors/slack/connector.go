// Package slack wraps the Slack Web API as a wick connector.
// One instance = one Slack workspace (bot token). Operations cover
// the most common LLM-driven workflows: listing channels, sending
// messages, looking up users, and uploading files.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, thin handlers
//   - service.go   — URL construction, response validation
//   - repo.go      — outbound HTTP via http.NewRequestWithContext
package slack

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const Key = "slack"

const apiBase = "https://slack.com/api"

// Configs is the per-instance credential set.
type Configs struct {
	BotToken string `wick:"secret;required;desc=Slack Bot Token. Starts with xoxb-. Needs channels:read, chat:write, users:read, files:write scopes."`
}

// ListChannelsInput lists public channels in the workspace.
type ListChannelsInput struct {
	Limit          int    `wick:"desc=Max channels to return, max 1000. Default: 100."`
	ExcludeArchived bool  `wick:"desc=Skip archived channels. Default: true."`
}

// SendMessageInput posts a message to a channel.
type SendMessageInput struct {
	Channel string `wick:"required;desc=Channel ID or name (e.g. C012AB3CD or #general)."`
	Text    string `wick:"textarea;required;desc=Message text. Markdown and mrkdwn formatting supported."`
	AsUser  bool   `wick:"desc=Post as the authed user instead of the bot. Default: false."`
}

// GetUserInput fetches a user's profile by ID.
type GetUserInput struct {
	UserID string `wick:"required;desc=Slack user ID. Example: U012AB3CDE"`
}

// UploadFileInput uploads a file and shares it to a channel.
type UploadFileInput struct {
	Channel  string `wick:"required;desc=Channel ID or name to share the file into."`
	Content  string `wick:"textarea;required;desc=File content as plain text."`
	Filename string `wick:"required;desc=File name with extension. Example: report.txt"`
	Title    string `wick:"desc=Display title for the file. Defaults to filename."`
}

// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Slack",
		Description: "List channels, send messages, look up users, and upload files in a Slack workspace via the Web API.",
		Icon:        "💬",
	}
}

// Operations returns the LLM-callable actions for this connector.
func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"list_channels",
			"List Channels",
			"List public channels in the workspace. Returns channel ID, name, topic, purpose, and member count.",
			ListChannelsInput{},
			listChannels,
		),
		connector.OpDestructive(
			"send_message",
			"Send Message",
			"Post a message to a Slack channel. Returns the timestamp and channel of the posted message.",
			SendMessageInput{},
			sendMessage,
		),
		connector.Op(
			"get_user",
			"Get User",
			"Fetch a user's display name, real name, email, and status by their Slack user ID.",
			GetUserInput{},
			getUser,
		),
		connector.OpDestructive(
			"upload_file",
			"Upload File",
			"Upload a plain-text file and share it into a channel. Returns the file ID and permalink.",
			UploadFileInput{},
			uploadFile,
		),
	}
}

// ── Operation handlers ───────────────────────────────────────────────

func listChannels(c *connector.Ctx) (any, error) {
	limit := firstNonZero(c.InputInt("limit"), 100)
	exclude := "true"
	if c.Input("exclude_archived") == "false" {
		exclude = "false"
	}
	url := fmt.Sprintf("%s/conversations.list?limit=%d&exclude_archived=%s&types=public_channel", apiBase, limit, exclude)
	resp, err := doRequest(c, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return slackField(resp, "channels")
}

func sendMessage(c *connector.Ctx) (any, error) {
	channel := strings.TrimSpace(c.Input("channel"))
	text := strings.TrimSpace(c.Input("text"))
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}
	body := map[string]any{
		"channel": channel,
		"text":    text,
	}
	resp, err := doRequest(c, "POST", apiBase+"/chat.postMessage", body)
	if err != nil {
		return nil, err
	}
	return pickFields(resp, "ok", "ts", "channel", "message")
}

func getUser(c *connector.Ctx) (any, error) {
	userID := strings.TrimSpace(c.Input("user_id"))
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	url := fmt.Sprintf("%s/users.info?user=%s", apiBase, userID)
	resp, err := doRequest(c, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return slackField(resp, "user")
}

func uploadFile(c *connector.Ctx) (any, error) {
	channel := strings.TrimSpace(c.Input("channel"))
	content := c.Input("content")
	filename := strings.TrimSpace(c.Input("filename"))
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}
	if filename == "" {
		return nil, fmt.Errorf("filename is required")
	}
	title := firstNonEmpty(strings.TrimSpace(c.Input("title")), filename)
	body := map[string]any{
		"channels": channel,
		"content":  content,
		"filename": filename,
		"title":    title,
	}
	resp, err := doRequest(c, "POST", apiBase+"/files.upload", body)
	if err != nil {
		return nil, err
	}
	return slackField(resp, "file")
}
