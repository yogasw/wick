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
	// enabled mirrors whether the primary credential is present. Default
	// signal is a non-empty "bot_token"; channels with no per-instance
	// token (e.g. REST, which authenticates via PAT per request) use an
	// explicit "enabled" key in the JSON config instead.
	enabled := m["bot_token"] != ""
	if _, hasEnabled := m["enabled"]; hasEnabled {
		enabled = m["enabled"] == "true"
	}
	return db.Model(&ch).Updates(map[string]interface{}{
		"config":     string(data),
		"enabled":    enabled,
		"updated_at": time.Now(),
	}).Error
}

// firstNonEmpty returns v when non-empty, otherwise fallback. Used to
// supply per-list mode defaults on legacy rows that pre-date the new keys.
func firstNonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// LoadSlackConfig reads the Slack channel config from agent_channels.
// Returns zero value + empty pubURL when no row exists.
func LoadSlackConfig(db *gorm.DB) (cfg agentconfig.SlackChannelConfig, pubURL string, err error) {
	m, err := GetChannelConfigMap(db, "slack")
	if err != nil {
		return
	}
	cfg = agentconfig.SlackChannelConfig{
		Mode:               m["mode"],
		BotToken:           m["bot_token"],
		AppToken:           m["app_token"],
		SigningSecret:      m["signing_secret"],
		UsersMode:          firstNonEmpty(m["users_mode"], "all"),
		AllowedUsers:       m["allowed_users"],
		GroupsMode:         firstNonEmpty(m["groups_mode"], "all"),
		AllowedGroups:      m["allowed_groups"],
		ChannelsMode:       firstNonEmpty(m["channels_mode"], "all"),
		AllowedChannels:    m["allowed_channels"],
		GateApprovers:      firstNonEmpty(m["gate_approvers"], "trigger_users"),
		GateApproverUsers:  m["gate_approver_users"],
		GateApproverGroups: m["gate_approver_groups"],
		Workspace:          m["workspace"],
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

// LoadRestConfig reads the REST channel config from agent_channels.
func LoadRestConfig(db *gorm.DB) (agentconfig.RestChannelConfig, error) {
	m, err := GetChannelConfigMap(db, "rest")
	if err != nil {
		return agentconfig.RestChannelConfig{}, err
	}
	return agentconfig.RestChannelConfig{
		Enabled:   m["enabled"],
		Workspace: m["workspace"],
	}, nil
}

// DBStore satisfies SlackConfigStore + TelegramConfigStore by delegating
// to the package-level Load*Config helpers. Server wires one of these at
// boot so per-channel ConfigSource implementations don't need to import
// gorm directly.
type DBStore struct{ db *gorm.DB }

// NewDBStore returns a DBStore bound to db.
func NewDBStore(db *gorm.DB) DBStore { return DBStore{db: db} }

// LoadSlack satisfies SlackConfigStore.
func (s DBStore) LoadSlack() (agentconfig.SlackChannelConfig, string, error) {
	return LoadSlackConfig(s.db)
}

// LoadTelegram satisfies TelegramConfigStore.
func (s DBStore) LoadTelegram() (agentconfig.TelegramChannelConfig, error) {
	return LoadTelegramConfig(s.db)
}

// LoadRest satisfies RestConfigStore.
func (s DBStore) LoadRest() (agentconfig.RestChannelConfig, error) {
	return LoadRestConfig(s.db)
}

// EnsureChannel satisfies ChannelEnsurer.
func (s DBStore) EnsureChannel(channelType string) error {
	return EnsureChannel(s.db, channelType)
}
