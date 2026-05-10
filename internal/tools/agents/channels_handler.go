// Package agents — channel config UI handlers.
//
// Purpose:     Renders and saves Slack / Telegram channel config pages.
//              Config values are stored in agent_channels table (one row per
//              channel type, JSON blob), not in the generic configs table.
// Caller:      handler.go Register() mounts routes under /channels/*.
// Dependencies:
//   - internal/agents/channels (store helpers)
//   - internal/agents/config   (seed / typed config)
//   - internal/agents/workspace (workspace list for dropdown)
//   - internal/entity           (entity.Config for ConfigsTable UI)
//
// Main Functions:
//   - channelsPage        — list page (Slack, Telegram cards)
//   - slackChannelPage    — Slack config form
//   - telegramChannelPage — Telegram config form
//   - saveChannelConfig   — POST handler for one key update
//   - loadChannelRows     — merge seed + DB values into UI rows
//
// Side Effects: Reads/writes agent_channels table via store helpers.

package agents

import (
	"net/http"
	"strings"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentworkspace "github.com/yogasw/wick/internal/agents/workspace"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// channelsPage renders the list of available channels (Slack, Telegram).
func channelsPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	base := c.Base()
	channels := []view.ChannelVM{
		{
			Name:        "Slack",
			Slug:        "slack",
			Icon:        "💬",
			Description: "Connect via Slack Socket Mode or HTTP. Agents reply in-thread and support @mentions.",
			HRef:        base + "/channels/slack",
		},
		{
			Name:        "Telegram",
			Slug:        "telegram",
			Icon:        "✈️",
			Description: "Connect via Telegram Bot API. Agents reply in private chats and group threads.",
			HRef:        base + "/channels/telegram",
		},
	}
	// Populate Configured status from agent_channels table.
	if globalDB != nil {
		for i := range channels {
			m, _ := agentchannels.GetChannelConfigMap(globalDB, channels[i].Slug)
			channels[i].Configured = m["bot_token"] != ""
		}
	}
	c.HTML(view.ChannelListPage(view.ChannelListVM{Base: base, Channels: channels}))
}

// slackChannelPage renders the Slack channel config form.
func slackChannelPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	rows := loadChannelRows("slack", agentconfig.SeedSlackChannelConfig(), "workspace")
	c.HTML(view.ChannelConfigPage(view.ChannelConfigVM{
		Base:        c.Base(),
		ChannelName: "Slack",
		ChannelSlug: "slack",
		Rows:        rows,
		ActionBase:  c.Base() + "/channels/slack",
	}))
}

// telegramChannelPage renders the Telegram channel config form.
func telegramChannelPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	rows := loadChannelRows("telegram", agentconfig.SeedTelegramChannelConfig(), "workspace")
	c.HTML(view.ChannelConfigPage(view.ChannelConfigVM{
		Base:        c.Base(),
		ChannelName: "Telegram",
		ChannelSlug: "telegram",
		Rows:        rows,
		ActionBase:  c.Base() + "/channels/telegram",
	}))
}

// makeChannelSaveHandler returns a POST handler for /channels/{channelType}/{key}
// that saves one config value for channelType in the agent_channels table.
func makeChannelSaveHandler(channelType string) func(*tool.Ctx) {
	return func(c *tool.Ctx) {
		if notReady(c) {
			return
		}
		if globalDB == nil {
			c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "db not ready"})
			return
		}
		key := c.PathValue("key")
		value := c.Form("value")
		if err := agentchannels.SetChannelConfigKey(globalDB, channelType, key, value); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// loadChannelRows returns entity.Config rows (for the ConfigsTable UI component)
// with values populated from the agent_channels JSON config.
// workspaceKey is the key whose Options should be set to the live workspace list.
func loadChannelRows(channelType string, seed []entity.Config, workspaceKey string) []entity.Config {
	rows := make([]entity.Config, len(seed))
	copy(rows, seed)

	// Inject current values from agent_channels config JSON.
	if globalDB != nil {
		m, _ := agentchannels.GetChannelConfigMap(globalDB, channelType)
		for i := range rows {
			if v, ok := m[rows[i].Key]; ok {
				rows[i].Value = v
			}
		}
	}

	// Populate workspace dropdown options.
	if globalLayout.BaseDir != "" && workspaceKey != "" {
		wsNames, err := agentworkspace.List(globalLayout)
		if err == nil && len(wsNames) > 0 {
			for i := range rows {
				if rows[i].Key == workspaceKey {
					rows[i].Options = strings.Join(wsNames, "|")
				}
			}
		}
	}

	return rows
}
