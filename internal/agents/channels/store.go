// Package channels — agent_channels DB helpers.
//
// Purpose:     CRUD helpers for the agent_channels table. Each channel type
//              (slack, telegram) gets one row whose Config column is a JSON
//              map[string]string of its per-field settings. Per-user variants
//              allow each user to have their own channel row (user_id = NULL
//              for the App Owner row).
// Caller:      channels_handler.go (UI save/load), server.go (boot load).
// Dependencies:
//   - gorm.io/gorm
//   - github.com/google/uuid
//   - internal/entity (AgentChannel)
//   - internal/agents/config (SlackChannelConfig, TelegramChannelConfig)
//
// Main Functions:
//   - EnsureChannel              — idempotent row creation (App Owner)
//   - EnsureChannelForUser       — idempotent row creation per user
//   - GetChannelConfigMap        — load JSON config as map (App Owner)
//   - GetChannelConfigMapForUser — load JSON config as map per user
//   - SetChannelConfigKey        — update one key in JSON config (App Owner)
//   - SetChannelConfigKeyForUser — update one key in JSON config per user
//   - ListChannelOwners          — list all enabled UserID values for a type
//   - LoadSlackConfig            — typed load for Slack (App Owner)
//   - LoadTelegramConfig         — typed load for Telegram (App Owner)
//
// Side Effects: Writes to agent_channels table.

package channels

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	pkgentity "github.com/yogasw/wick/pkg/entity"
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

// EnsureChannelForUser creates a channel row for a specific user if it doesn't exist.
// Pass userID="" to create/ensure the App Owner row (user_id = NULL).
func EnsureChannelForUser(db *gorm.DB, channelType, userID string) error {
	var ch entity.AgentChannel
	var err error
	if userID == "" {
		err = db.Where("type = ? AND user_id IS NULL", channelType).First(&ch).Error
	} else {
		err = db.Where("type = ? AND user_id = ?", channelType, userID).First(&ch).Error
	}
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var uid *string
	if userID != "" {
		uid = &userID
	}
	ch = entity.AgentChannel{
		ID:        uuid.New().String(),
		Type:      channelType,
		Name:      "default",
		UserID:    uid,
		Enabled:   false,
		Config:    "{}",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&ch).Error
}

// GetChannelConfigMapForUser loads the JSON config for one user's channel row.
// Pass userID="" for the App Owner row (user_id = NULL).
func GetChannelConfigMapForUser(db *gorm.DB, channelType, userID string) (map[string]string, error) {
	var ch entity.AgentChannel
	var err error
	if userID == "" {
		err = db.Where("type = ? AND user_id IS NULL", channelType).First(&ch).Error
	} else {
		err = db.Where("type = ? AND user_id = ?", channelType, userID).First(&ch).Error
	}
	if err != nil {
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

// SetChannelConfigKeyForUser updates one key in a user's channel config.
// Creates the row if it doesn't exist. Pass userID="" for App Owner.
func SetChannelConfigKeyForUser(db *gorm.DB, channelType, userID, key, value string) error {
	if err := EnsureChannelForUser(db, channelType, userID); err != nil {
		return err
	}
	var ch entity.AgentChannel
	var err error
	if userID == "" {
		err = db.Where("type = ? AND user_id IS NULL", channelType).First(&ch).Error
	} else {
		err = db.Where("type = ? AND user_id = ?", channelType, userID).First(&ch).Error
	}
	if err != nil {
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

// ListChannelOwners returns all distinct UserID values (including nil for App Owner)
// for the given channel type that have an enabled row.
func ListChannelOwners(db *gorm.DB, channelType string) ([]*string, error) {
	var rows []entity.AgentChannel
	if err := db.Where("type = ? AND enabled = ?", channelType, true).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*string, len(rows))
	for i, r := range rows {
		out[i] = r.UserID
	}
	return out, nil
}

// DBStore satisfies SlackConfigStore + TelegramConfigStore by delegating
// to configMap so callers always receive decrypted plaintext values. Server
// wires one of these at
// boot so per-channel ConfigSource implementations don't need to import
// gorm directly.
// Configs is optional — when set, wick_cenc_ tokens in the JSON config
// are decrypted before being returned to callers.
type DBStore struct {
	db      *gorm.DB
	Configs *configs.Service
}

// NewDBStore returns a DBStore bound to db.
func NewDBStore(db *gorm.DB) DBStore { return DBStore{db: db} }

// configMap loads, decrypts, and maps the JSON config for channelType
// directly into dst (pointer to a config struct).
func (s DBStore) configMap(channelType string, dst any) error {
	m, err := GetChannelConfigMap(s.db, channelType)
	if err != nil {
		return err
	}
	if s.Configs != nil {
		for k, v := range m {
			if plain, err := s.Configs.DecryptSecret(v); err == nil {
				m[k] = plain
			}
		}
	}
	pkgentity.MapToStruct(m, dst)
	return nil
}

// LoadSlack satisfies SlackConfigStore.
func (s DBStore) LoadSlack() (agentconfig.SlackChannelConfig, string, error) {
	var cfg agentconfig.SlackChannelConfig
	if err := s.configMap("slack", &cfg); err != nil {
		return cfg, "", err
	}
	return cfg, cfg.PublicURL, nil
}

// LoadTelegram satisfies TelegramConfigStore.
func (s DBStore) LoadTelegram() (agentconfig.TelegramChannelConfig, error) {
	var cfg agentconfig.TelegramChannelConfig
	return cfg, s.configMap("telegram", &cfg)
}

// LoadRest satisfies RestConfigStore.
func (s DBStore) LoadRest() (agentconfig.RestChannelConfig, error) {
	var cfg agentconfig.RestChannelConfig
	return cfg, s.configMap("rest", &cfg)
}

// EnsureChannel satisfies ChannelEnsurer.
func (s DBStore) EnsureChannel(channelType string) error {
	return EnsureChannel(s.db, channelType)
}

// configMapForUser loads, decrypts, and maps the JSON config for a specific user's channel into dst.
func (s DBStore) configMapForUser(channelType, userID string, dst any) error {
	m, err := GetChannelConfigMapForUser(s.db, channelType, userID)
	if err != nil {
		return err
	}
	if s.Configs != nil {
		for k, v := range m {
			if plain, err := s.Configs.DecryptSecret(v); err == nil {
				m[k] = plain
			}
		}
	}
	pkgentity.MapToStruct(m, dst)
	return nil
}

// LoadSlackForUser loads the Slack config for a specific user.
// Pass userID="" for the App Owner row.
func (s DBStore) LoadSlackForUser(userID string) (agentconfig.SlackChannelConfig, string, error) {
	var cfg agentconfig.SlackChannelConfig
	if err := s.configMapForUser("slack", userID, &cfg); err != nil {
		return cfg, "", err
	}
	return cfg, cfg.PublicURL, nil
}

// LoadTelegramForUser loads the Telegram config for a specific user.
func (s DBStore) LoadTelegramForUser(userID string) (agentconfig.TelegramChannelConfig, error) {
	var cfg agentconfig.TelegramChannelConfig
	return cfg, s.configMapForUser("telegram", userID, &cfg)
}

// LoadRestForUser loads the REST config for a specific user.
func (s DBStore) LoadRestForUser(userID string) (agentconfig.RestChannelConfig, error) {
	var cfg agentconfig.RestChannelConfig
	return cfg, s.configMapForUser("rest", userID, &cfg)
}

// ListChannelOwners satisfies the setup.SlackStore interface.
func (s DBStore) ListChannelOwners(channelType string) ([]*string, error) {
	return ListChannelOwners(s.db, channelType)
}

// EnsureChannelForUser satisfies the setup interface.
func (s DBStore) EnsureChannelForUser(channelType, userID string) error {
	return EnsureChannelForUser(s.db, channelType, userID)
}
