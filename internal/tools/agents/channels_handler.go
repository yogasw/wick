// Package agents — channel config UI handlers.
//
// Purpose:     Renders and saves Slack / Telegram channel config pages.
//              Config values are stored in agent_channels table (one row per
//              channel type + user, JSON blob), not in the generic configs table.
// Caller:      handler.go Register() mounts routes under /channels/*.
// Dependencies:
//   - internal/agents/channels (store helpers)
//   - internal/agents/config   (seed / typed config)
//   - internal/agents/project   (project list for dropdown)
//   - internal/entity           (entity.Config for ConfigsTable UI)
//
// Main Functions:
//   - channelsPage           — list page (Slack, Telegram cards)
//   - slackChannelPage       — Slack config form
//   - telegramChannelPage    — Telegram config form
//   - makeChannelSaveHandler — POST handler for one key update (per-user)
//   - loadChannelRowsForUser — merge seed + DB values (per-user row)
//   - syncChannelInstance    — hot-add/reload registry entry after save
//
// Side Effects: Reads/writes agent_channels table via store helpers.

package agents

import (
	"context"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentrest "github.com/yogasw/wick/internal/agents/channels/rest"
	channelsetup "github.com/yogasw/wick/internal/agents/channels/setup"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	agenttelegram "github.com/yogasw/wick/internal/agents/channels/telegram"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentproject "github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// currentUserIDForChannel returns the userID key to use for per-user channel
// config lookups. App Owner users return "" (owner row).
func currentUserIDForChannel(c *tool.Ctx) string {
	u := login.GetUser(c.Context())
	if u == nil || u.IsOwner {
		return ""
	}
	return u.ID
}

// channelProjectAccess builds the project visibility filter for the logged-in
// user, mirroring the app-wide access rule (admin/owner sees all, otherwise
// own + shared + tag-shared).
func channelProjectAccess(c *tool.Ctx) agentproject.Access {
	u := login.GetUser(c.Context())
	acc := agentproject.Access{TagIDs: login.GetUserTagIDs(c.Context())}
	if u != nil {
		acc.UserID = u.ID
		acc.IsAdmin = u.IsAdmin()
	}
	return acc
}

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
	if globalDB != nil {
		userID := currentUserIDForChannel(c)
		for i := range channels {
			m, _ := agentchannels.GetChannelConfigMapForUser(globalDB, channels[i].Slug, userID)
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
	userID := currentUserIDForChannel(c)
	rows := loadChannelRowsForUser("slack", userID, agentconfig.SeedSlackChannelConfig(), "project_id", channelProjectAccess(c))
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
	userID := currentUserIDForChannel(c)
	rows := loadChannelRowsForUser("rest", userID, agentconfig.SeedRestChannelConfig(), "project_id", channelProjectAccess(c))

	appURL := ""
	if globalConfigs != nil {
		appURL = strings.TrimRight(globalConfigs.AppURL(), "/")
	}
	apiBase := appURL + "/integrations/rest/api/v1/openai"

	c.HTML(view.ChannelConfigPage(view.ChannelConfigVM{
		Layout:      sidebarVM(c, "channels", ""),
		Base:        c.Base(),
		ChannelName: "REST (OpenAI-compatible)",
		ChannelSlug: "rest",
		Rows:        rows,
		ActionBase:  c.Base() + "/channels/rest",
		Docs: view.RestDocs(view.RestDocsVM{
			Base:              appURL,
			APIBase:           apiBase,
			ChatEndpoint:      apiBase + "/chat/completions",
			ResponsesEndpoint: apiBase + "/responses",
			ModelsEndpoint:    apiBase + "/models",
		}),
	}))
}

// telegramChannelPage renders the Telegram channel config form.
func telegramChannelPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	userID := currentUserIDForChannel(c)
	rows := loadChannelRowsForUser("telegram", userID, agentconfig.SeedTelegramChannelConfig(), "project_id", channelProjectAccess(c))
	c.HTML(view.ChannelConfigPage(view.ChannelConfigVM{
		Layout:      sidebarVM(c, "channels", ""),
		Base:        c.Base(),
		ChannelName: "Telegram",
		ChannelSlug: "telegram",
		Rows:        rows,
		ActionBase:  c.Base() + "/channels/telegram",
	}))
}

// channelForUser returns the registry channel instance that belongs to the
// current user. Instance key is "<slug>:<userID>" or "<slug>:__owner__" for
// App Owner. Falls back to ChannelByName so Telegram/REST (single-instance)
// still resolve correctly.
func channelForUser(c *tool.Ctx, slug string) agentchannels.Channel {
	if globalChannels == nil {
		return nil
	}
	userID := currentUserIDForChannel(c)
	iKey := slug + ":" + userID
	if userID == "" {
		iKey = slug + ":__owner__"
	}
	if ch := globalChannels.ChannelByKey(iKey); ch != nil {
		return ch
	}
	// Per-user channel types (e.g. slack) use AddKeyed — don't fall back to
	// another user's instance when this user has no row yet.
	// Single-instance types (telegram, rest) use Add — fall back is correct.
	if globalChannels.HasAnyKeyed(slug) {
		return nil
	}
	return globalChannels.ChannelByName(slug)
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
	ch := channelForUser(c, slug)
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
	ch := channelForUser(c, slug)
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

// channelStatusHandler returns the channel's identity + transport state
// (bot user id/name, workspace, mode, subscription). Powers the
// "Integration status" panel rendered under the test button.
func channelStatusHandler(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalChannels == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "channel registry not ready"})
		return
	}
	slug := c.PathValue("slug")
	ch := channelForUser(c, slug)
	if ch == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "channel not registered"})
		return
	}
	sr, ok := ch.(agentchannels.StatusReporter)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": "channel does not report status"})
		return
	}
	fields := sr.Status()
	if fields == nil {
		fields = []agentchannels.StatusField{}
	}
	c.JSON(http.StatusOK, fields)
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
// that saves one config value for the current user in the agent_channels table.
// Fields declared secret in the channel's config struct are encrypted at rest.
// Any logged-in user can configure their own channel (no admin gate).
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
		userID := currentUserIDForChannel(c)
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
		if err := agentchannels.SetChannelConfigKeyForUser(globalDB, channelType, userID, key, value); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		syncChannelInstance(c.R.Context(), channelType, userID)
		c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// loadChannelRowsForUser returns entity.Config rows with values populated from
// a specific user's agent_channels JSON config. App Owner users pass userID=""
// which falls back to the owner row. Secret tokens are decrypted before render.
func loadChannelRowsForUser(channelType, userID string, seed []entity.Config, projectKey string, acc agentproject.Access) []entity.Config {
	rows := make([]entity.Config, len(seed))
	copy(rows, seed)
	if globalDB != nil {
		m, _ := agentchannels.GetChannelConfigMapForUser(globalDB, channelType, userID)
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
	if globalLayout.BaseDir != "" && projectKey != "" {
		// Only projects this user may access populate the default-project
		// dropdown — own + shared + tag-shared (admins see all).
		ids, err := agentproject.ListVisibleTo(globalLayout, acc)
		if err == nil && len(ids) > 0 {
			var opts []string
			for _, id := range ids {
				p, lerr := agentproject.Load(globalLayout, id)
				if lerr != nil {
					continue
				}
				label := p.Meta.Name
				if label == "" {
					label = id
				}
				opts = append(opts, label+"::"+id)
			}
			if len(opts) > 0 {
				for i := range rows {
					if rows[i].Key == projectKey {
						rows[i].Options = strings.Join(opts, "|")
					}
				}
			}
		}
	}
	return rows
}

// syncChannelInstance adds or reloads the channel registry entry for a user
// after their config is saved. No-op when globalChannels is nil.
func syncChannelInstance(ctx context.Context, channelType, userID string) {
	if globalChannels == nil || globalDB == nil {
		return
	}
	store := agentchannels.NewDBStore(globalDB)
	store.Configs = globalConfigs
	iKey := channelType + ":" + userID
	if userID == "" {
		iKey = channelType + ":__owner__"
	}
	// Boot-time and hot-reload paths must agree on this prefix, so reuse the
	// same helper the setup composer uses ("slack-" for App Owner,
	// "slack-<userID>-" per user).
	sessPrefix := channelsetup.SessionPrefix(channelType, userID)
	startInstance := func(ch agentchannels.Channel) {
		go func() {
			if err := ch.Start(ctx); err != nil {
				log.Warn().Str("instance", iKey).Err(err).Msg("agents: channel instance stopped")
			}
		}()
	}

	switch channelType {
	case "slack":
		cfg, pubURL, err := store.LoadSlackForUser(userID)
		if err != nil {
			return
		}
		if cfg.BotToken == "" {
			if globalChannels.HasKey(iKey) {
				globalChannels.RemoveKeyed(iKey)
			}
			return
		}
		if globalChannels.HasKey(iKey) {
			ch := globalChannels.ChannelByKey(iKey)
			if slackCh, ok := ch.(*agentslack.Channel); ok {
				slackCh.Reload(ctx, cfg, pubURL)
			}
		} else {
			ch := agentslack.NewWithOwner(cfg, userID)
			ch.SetSendFunc(globalChannels.SendFuncFor(channelType))
			ch.SetPublicURL(pubURL)
			ch.SetSessionPrefix(sessPrefix)
			src := agentslack.NewConfigSourceKeyed(store, ch, userID)
			globalChannels.AddKeyed(iKey, ch, src)
			startInstance(ch)
		}

	case "telegram":
		cfg, err := store.LoadTelegramForUser(userID)
		if err != nil {
			return
		}
		if cfg.BotToken == "" {
			if globalChannels.HasKey(iKey) {
				globalChannels.RemoveKeyed(iKey)
			}
			return
		}
		if globalChannels.HasKey(iKey) {
			ch := globalChannels.ChannelByKey(iKey)
			if tgCh, ok := ch.(*agenttelegram.Channel); ok {
				tgCh.Reload(ctx, cfg)
			}
		} else {
			ch := agenttelegram.NewWithOwner(cfg, userID)
			ch.SetSendFunc(globalChannels.SendFuncFor(channelType))
			ch.SetSessionPrefix(sessPrefix)
			src := agenttelegram.NewConfigSourceKeyed(store, ch, userID)
			globalChannels.AddKeyed(iKey, ch, src)
			startInstance(ch)
		}

	case "rest":
		cfg, err := store.LoadRestForUser(userID)
		if err != nil {
			return
		}
		if cfg.Enabled != "true" {
			if globalChannels.HasKey(iKey) {
				globalChannels.RemoveKeyed(iKey)
			}
			return
		}
		if globalChannels.HasKey(iKey) {
			ch := globalChannels.ChannelByKey(iKey)
			if restCh, ok := ch.(*agentrest.Channel); ok {
				restCh.Reload(ctx, cfg)
			}
		} else {
			// REST needs the boot-time Authenticator — reuse it from any
			// existing rest instance in the registry.
			auth := existingRestAuth()
			if auth == nil {
				log.Warn().Str("instance", iKey).Msg("agents: cannot create rest instance, no authenticator available")
				return
			}
			ch := agentrest.NewWithOwner(cfg, auth, userID)
			ch.SetSendFunc(globalChannels.SendFuncFor(channelType))
			src := agentrest.NewConfigSourceKeyed(store, ch, userID)
			globalChannels.AddKeyed(iKey, ch, src)
			startInstance(ch)
		}
	}
}

// existingRestAuth pulls the Authenticator from any rest instance already
// registered at boot, so a newly-created per-user rest instance can reuse it.
func existingRestAuth() agentrest.Authenticator {
	if globalChannels == nil {
		return nil
	}
	if ch := globalChannels.ChannelByKey("rest:__owner__"); ch != nil {
		if restCh, ok := ch.(*agentrest.Channel); ok {
			return restCh.Auth()
		}
	}
	for _, ch := range globalChannels.Channels() {
		if restCh, ok := ch.(*agentrest.Channel); ok {
			if a := restCh.Auth(); a != nil {
				return a
			}
		}
	}
	return nil
}
