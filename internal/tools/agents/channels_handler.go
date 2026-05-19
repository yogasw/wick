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
		{
			Name:        "REST",
			Slug:        "rest",
			Icon:        "🌐",
			Description: "OpenAI Chat Completions compatible HTTP endpoint. Use any OpenAI SDK with a Personal Access Token.",
			HRef:        base + "/channels/rest",
		},
	}
	// Populate Configured status from agent_channels table.
	if globalDB != nil {
		for i := range channels {
			m, _ := agentchannels.GetChannelConfigMap(globalDB, channels[i].Slug)
			if channels[i].Slug == "rest" {
				channels[i].Configured = m["enabled"] == "true"
			} else {
				channels[i].Configured = m["bot_token"] != ""
			}
		}
	}
	c.HTML(view.ChannelListPage(view.ChannelListVM{Layout: sidebarVM(c, "channels", ""), Base: base, Channels: channels}))
}

// slackChannelPage renders the Slack channel config form.
func slackChannelPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	rows := loadChannelRows("slack", agentconfig.SeedSlackChannelConfig(), "workspace")
	c.HTML(view.ChannelConfigPage(view.ChannelConfigVM{
		Layout:      sidebarVM(c, "channels", ""),
		Base:        c.Base(),
		ChannelName: "Slack",
		ChannelSlug: "slack",
		Rows:        rows,
		ActionBase:  c.Base() + "/channels/slack",
	}))
}

// restChannelPage renders the REST (OpenAI-compatible) channel config form
// plus a docs panel with sample curl / SDK usage.
func restChannelPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	rows := loadChannelRows("rest", agentconfig.SeedRestChannelConfig(), "workspace")

	appURL := ""
	if globalConfigs != nil {
		appURL = strings.TrimRight(globalConfigs.AppURL(), "/")
	}
	endpoint := appURL + "/integrations/rest/v1/chat/completions"

	c.HTML(view.ChannelConfigPage(view.ChannelConfigVM{
		Layout:      sidebarVM(c, "channels", ""),
		Base:        c.Base(),
		ChannelName: "REST (OpenAI-compatible)",
		ChannelSlug: "rest",
		Rows:        rows,
		ActionBase:  c.Base() + "/channels/rest",
		Docs: view.RestDocs(view.RestDocsVM{
			Base:       appURL,
			Endpoint:   endpoint,
			SampleUser: "demo-user",
		}),
	}))
}

// telegramChannelPage renders the Telegram channel config form.
func telegramChannelPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	rows := loadChannelRows("telegram", agentconfig.SeedTelegramChannelConfig(), "workspace")
	c.HTML(view.ChannelConfigPage(view.ChannelConfigVM{
		Layout:      sidebarVM(c, "channels", ""),
		Base:        c.Base(),
		ChannelName: "Telegram",
		ChannelSlug: "telegram",
		Rows:        rows,
		ActionBase:  c.Base() + "/channels/telegram",
	}))
}

// channelLookupHandler routes a picker search to the named channel's
// LookupProvider. URL: GET /channels/{slug}/lookup?source=<src>&q=<query>.
// Returns JSON array of {id,name}.
func channelLookupHandler(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalChannels == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "channel registry not ready"})
		return
	}
	slug := c.PathValue("slug")
	source := c.Query("source")
	query := c.Query("q")
	if slug == "" || source == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "slug and source required"})
		return
	}
	ch := globalChannels.ChannelByName(slug)
	if ch == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "channel not registered"})
		return
	}
	lp, ok := ch.(agentchannels.LookupProvider)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": "channel does not support lookup"})
		return
	}
	items, err := lp.Lookup(source, query)
	if err != nil {
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if items == nil {
		items = []agentchannels.LookupItem{}
	}
	c.JSON(http.StatusOK, items)
}

// channelHealthHandler runs the channel's self-test probes (auth, list,
// search, write) so the operator can verify scopes/credentials from the
// admin UI without booting the agent loop. Returns JSON array of checks.
func channelHealthHandler(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalChannels == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "channel registry not ready"})
		return
	}
	slug := c.PathValue("slug")
	ch := globalChannels.ChannelByName(slug)
	if ch == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "channel not registered"})
		return
	}
	hc, ok := ch.(agentchannels.HealthChecker)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": "channel does not support health check"})
		return
	}
	checks := hc.HealthCheck()
	if checks == nil {
		checks = []agentchannels.HealthCheck{}
	}
	c.JSON(http.StatusOK, checks)
}

// channelSecretKeys returns the set of secret key names for a channel type,
// derived from the wick:"secret" tag on each channel's config struct.
func channelSecretKeys(channelType string) map[string]bool {
	var seed []entity.Config
	switch channelType {
	case "slack":
		seed = agentconfig.SeedSlackChannelConfig()
	case "telegram":
		seed = agentconfig.SeedTelegramChannelConfig()
	}
	m := make(map[string]bool, len(seed))
	for _, r := range seed {
		if r.IsSecret {
			m[r.Key] = true
		}
	}
	return m
}

// makeChannelSaveHandler returns a POST handler for /channels/{channelType}/{key}
// that saves one config value for channelType in the agent_channels table.
// Fields declared secret in the channel's config struct are encrypted at rest.
func makeChannelSaveHandler(channelType string) func(*tool.Ctx) {
	secretKeys := channelSecretKeys(channelType)
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
		if value != "" && secretKeys[key] && globalConfigs != nil {
			encrypted, err := globalConfigs.EncryptSecret(value)
			if err != nil {
				c.JSON(http.StatusInternalServerError, map[string]string{"error": "encrypt: " + err.Error()})
				return
			}
			value = encrypted
		}
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
// Secret values stored as wick_cenc_ tokens are decrypted before render so the
// UI can show the "stored" badge correctly (non-empty value = stored).
func loadChannelRows(channelType string, seed []entity.Config, workspaceKey string) []entity.Config {
	rows := make([]entity.Config, len(seed))
	copy(rows, seed)

	// Inject current values from agent_channels config JSON.
	if globalDB != nil {
		m, _ := agentchannels.GetChannelConfigMap(globalDB, channelType)
		for i := range rows {
			v, ok := m[rows[i].Key]
			if !ok {
				continue
			}
			if rows[i].IsSecret && globalConfigs != nil {
				plain, err := globalConfigs.DecryptSecret(v)
				if err == nil {
					v = plain
				}
			}
			rows[i].Value = v
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
