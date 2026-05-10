// Package channels — agent_channels DB helpers.
//
// Purpose:     CRUD helpers for the agent_channels table. Each channel type
//              (slack, telegram) gets one row whose Config column is a JSON
//              map[string]string of its per-field settings.
// Caller:      channels_handler.go (UI save/load), server.go (boot load).
// Dependencies:
//   - gorm.io/gorm
//   - github.com/google/uuid
//   - internal/entity (AgentChannel)
//   - internal/agents/config (SlackChannelConfig, TelegramChannelConfig)
//
// Main Functions:
//   - EnsureChannel         — idempotent row creation
//   - GetChannelConfigMap   — load JSON config as map
//   - SetChannelConfigKey   — update one key in JSON config
//   - LoadSlackConfig       — typed load for Slack
//   - LoadTelegramConfig    — typed load for Telegram
//
// Side Effects: Writes to agent_channels table.

package channels

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureChannel creates a default agent_channels row for channelType if
// none exists. Safe to call at boot — idempotent.
func EnsureChannel(db *gorm.DB, channelType string) error {
	var ch entity.AgentChannel
	err := db.Where("type = ?", channelType).First(&ch).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	ch = entity.AgentChannel{
		ID:        uuid.New().String(),
		Type:      channelType,
		Name:      "default",
		Enabled:   false,
		Config:    "{}",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&ch).Error
}

// GetChannelConfigMap loads the JSON config for a channel type into a map.
// Returns an empty map when no row exists (not an error).
func GetChannelConfigMap(db *gorm.DB, channelType string) (map[string]string, error) {
	var ch entity.AgentChannel
	if err := db.Where("type = ?", channelType).First(&ch).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	m := map[string]string{}
	if ch.Config != "" && ch.Config != "{}" {
		if err := json.Unmarshal([]byte(ch.Config), &m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// SetChannelConfigKey updates a single key inside the channel's JSON config.
// Creates the row first if it doesn't exist.
func SetChannelConfigKey(db *gorm.DB, channelType, key, value string) error {
	if err := EnsureChannel(db, channelType); err != nil {
		return err
	}
	var ch entity.AgentChannel
	if err := db.Where("type = ?", channelType).First(&ch).Error; err != nil {
		return err
	}
	m := map[string]string{}
	if ch.Config != "" && ch.Config != "{}" {
		if err := json.Unmarshal([]byte(ch.Config), &m); err != nil {
			return err
		}
	}
	m[key] = value
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	// enabled mirrors whether a bot_token is present — the UI reads this
	// for the "Configured" badge. Any channel type that uses a different
	// primary credential key (not "bot_token") must override this field
	// after calling SetChannelConfigKey.
	return db.Model(&ch).Updates(map[string]interface{}{
		"config":     string(data),
		"enabled":    m["bot_token"] != "",
		"updated_at": time.Now(),
	}).Error
}

// LoadSlackConfig reads the Slack channel config from agent_channels.
// Returns zero value + empty pubURL when no row exists.
func LoadSlackConfig(db *gorm.DB) (cfg agentconfig.SlackChannelConfig, pubURL string, err error) {
	m, err := GetChannelConfigMap(db, "slack")
	if err != nil {
		return
	}
	cfg = agentconfig.SlackChannelConfig{
		Mode:          m["mode"],
		BotToken:      m["bot_token"],
		AppToken:      m["app_token"],
		SigningSecret: m["signing_secret"],
		AccessMode:    m["access_mode"],
		AllowedUsers:  m["allowed_users"],
		AllowedGroups: m["allowed_groups"],
		Workspace:     m["workspace"],
	}
	pubURL = m["public_url"]
	return
}

// LoadTelegramConfig reads the Telegram channel config from agent_channels.
func LoadTelegramConfig(db *gorm.DB) (agentconfig.TelegramChannelConfig, error) {
	m, err := GetChannelConfigMap(db, "telegram")
	if err != nil {
		return agentconfig.TelegramChannelConfig{}, err
	}
	return agentconfig.TelegramChannelConfig{
		BotToken:   m["bot_token"],
		AllowedIDs: m["allowed_ids"],
		Workspace:  m["workspace"],
	}, nil
}
