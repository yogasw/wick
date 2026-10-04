// Package slackremote is the Slack adapter of remote.Source: an agent that
// lives in Slack (a third-party AI bot, an internal bot, a person) wrapped
// as a Team agent. wick posts the turn to a DM, a channel or a thread, as
// its bot or as the agent's creator, and reads the answer from the Slack
// events wick already receives, polling conversations.replies only while
// a turn waits.
package slackremote

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Kind is entity.AgentPersona.Kind of a Slack remote agent.
const Kind = "slack-remote"

// AdapterKind is its remote.Source kind.
const AdapterKind = "slack"

// ProviderKey is the provider of its sessions: no local CLI, the pool
// hands them to this adapter.
const ProviderKey = "slack-remote/slack-remote"

// RowType is the agent_channels type its settings are stored under.
const RowType = "slack-remote"

// ProjectTag marks the hidden project its sessions live in.
const ProjectTag = "wick:slack-remote"

// Identity values.
const (
	IdentityBot  = "bot"
	IdentityUser = "user"
)

// Target values.
const (
	TargetDM      = "dm"
	TargetChannel = "channel"
	TargetThread  = "thread"
)

// Listen values.
const (
	ListenTarget = "target"
	ListenAnyone = "anyone"
)

// Usage values, as for A2A remote agents.
const (
	UsageOnlyMe      = "only_me"
	UsageMeAndAgents = "me_and_my_agents"
	UsageByMention   = "mention"
)

const (
	DefaultIdleSec  = 30
	DefaultGraceSec = 120
	DefaultMaxSec   = 180
	MaxMaxSec       = 900
)

// Config is one Slack remote agent's settings. It holds no secret: the
// token is read from the connector (bot) or the creator's OAuth account
// (user) per spawn.
type Config struct {
	AgentID     string `json:"-"`
	OwnerUserID string `json:"-"`

	ConnectorID string `json:"connector_id"`
	Identity    string `json:"identity"`
	// AccountID is the connector account posted as when Identity is user;
	// it must belong to OwnerUserID.
	AccountID string `json:"account_id,omitempty"`

	Target string `json:"target"`
	// Channel is the channel of a channel/thread target.
	Channel string `json:"channel,omitempty"`
	// User is the user or bot user id a DM goes to.
	User string `json:"user,omitempty"`
	// MentionID is a user id @-mentioned at the start of a new channel
	// thread, for a bot that answers only when mentioned.
	MentionID string `json:"mention_id,omitempty"`
	// ThreadTS is the thread of a thread target.
	ThreadTS string `json:"thread_ts,omitempty"`
	// TargetName labels the target in the UI ("#ops", "@helper").
	TargetName string `json:"target_name,omitempty"`

	Listen string `json:"listen"`
	// Marker off = no end-marker instruction (a human on the other side).
	Marker *bool `json:"marker,omitempty"`
	// MentionTarget off = turns are posted as written. Unset (agents saved
	// before the setting existed) = on: many Slack bots answer only when
	// @-mentioned.
	MentionTarget *bool `json:"mention_target,omitempty"`
	IdleSec       int   `json:"idle_sec,omitempty"`
	MaxSec        int   `json:"max_sec,omitempty"`
	// PollSec caps the wait between two reads of the thread while no
	// event arrives. 0 = the runner's backoff (up to 10s).
	PollSec int `json:"poll_sec,omitempty"`
	// GraceSec is how long a late message or edit after a turn ended is
	// still passed on. 0 = DefaultGraceSec, -1 = off.
	GraceSec int    `json:"grace_sec,omitempty"`
	Usage    string `json:"usage,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

// MarkerOn reports whether turns carry the end-marker instruction.
func (c Config) MarkerOn() bool { return c.Marker == nil || *c.Marker }

// MentionOn reports whether every turn starts with an @-mention of the
// target.
func (c Config) MentionOn() bool { return c.MentionTarget == nil || *c.MentionTarget }

// TargetID is the user or bot id turns @-mention: the DM user, or the
// mention id of a channel or thread target. "" = none known.
func (c Config) TargetID() string {
	if c.Target == TargetDM {
		return c.User
	}
	return c.MentionID
}

func (c Config) Idle() time.Duration {
	if c.IdleSec <= 0 {
		return DefaultIdleSec * time.Second
	}
	return time.Duration(c.IdleSec) * time.Second
}

// Grace is the window for late messages after a turn ended.
func (c Config) Grace() time.Duration {
	switch {
	case c.GraceSec < 0:
		return 0 // off
	case c.GraceSec == 0:
		return DefaultGraceSec * time.Second
	}
	return time.Duration(c.GraceSec) * time.Second
}

// Poll is the longest wait between two reads; 0 = no cap.
func (c Config) Poll() time.Duration { return time.Duration(c.PollSec) * time.Second }

func (c Config) Max() time.Duration {
	if c.MaxSec <= 0 {
		return DefaultMaxSec * time.Second
	}
	return time.Duration(c.MaxSec) * time.Second
}

func (c Config) EffectiveListen() string {
	if c.Listen == ListenAnyone {
		return ListenAnyone
	}
	return ListenTarget
}

func (c Config) EffectiveUsage() string {
	switch c.Usage {
	case UsageMeAndAgents, UsageByMention:
		return c.Usage
	}
	return UsageOnlyMe
}

// Normalize trims c and checks its shape: what each target needs, and the
// account an identity of user needs. Ownership of that account is the
// caller's to check (it needs the connectors service).
func (c *Config) Normalize() error {
	c.ConnectorID = strings.TrimSpace(c.ConnectorID)
	c.Channel = strings.TrimSpace(c.Channel)
	c.User = strings.TrimSpace(c.User)
	c.MentionID = strings.TrimSpace(c.MentionID)
	c.ThreadTS = strings.TrimSpace(c.ThreadTS)
	c.AccountID = strings.TrimSpace(c.AccountID)
	if c.ConnectorID == "" {
		return errors.New("connector_id is required")
	}
	switch c.Identity {
	case "", IdentityBot:
		c.Identity, c.AccountID = IdentityBot, ""
	case IdentityUser:
		if c.AccountID == "" {
			return errors.New("account_id is required to post as you")
		}
	default:
		return fmt.Errorf("identity must be %q or %q", IdentityBot, IdentityUser)
	}
	switch c.Target {
	case TargetDM:
		if c.User == "" {
			return errors.New("user is required for a DM target")
		}
		c.Channel, c.ThreadTS = "", ""
	case TargetChannel:
		if c.Channel == "" {
			return errors.New("channel is required for a channel target")
		}
		c.ThreadTS = ""
	case TargetThread:
		if c.Channel == "" || c.ThreadTS == "" {
			return errors.New("channel and thread_ts are required for a thread target")
		}
	default:
		return fmt.Errorf("target must be %q, %q or %q", TargetDM, TargetChannel, TargetThread)
	}
	switch c.Listen {
	case "", ListenTarget, ListenAnyone:
	default:
		return fmt.Errorf("listen must be %q or %q", ListenTarget, ListenAnyone)
	}
	switch c.Usage {
	case "", UsageOnlyMe, UsageMeAndAgents, UsageByMention:
	default:
		return fmt.Errorf("usage must be %q, %q or %q", UsageOnlyMe, UsageMeAndAgents, UsageByMention)
	}
	if c.GraceSec < -1 || c.GraceSec > MaxMaxSec {
		return fmt.Errorf("grace_sec must be between -1 (off) and %d", MaxMaxSec)
	}
	if c.PollSec < 0 || c.PollSec > MaxMaxSec {
		return fmt.Errorf("poll_sec must be between 0 and %d", MaxMaxSec)
	}
	if c.IdleSec < 0 || c.IdleSec > MaxMaxSec {
		return fmt.Errorf("idle_sec must be between 0 and %d", MaxMaxSec)
	}
	if c.MaxSec < 0 || c.MaxSec > MaxMaxSec {
		return fmt.Errorf("max_sec must be between 0 and %d", MaxMaxSec)
	}
	return nil
}

// Store keeps the settings on agent_channels rows.
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func RowID(agentID string) string { return RowType + ":" + agentID }

// Load returns agentID's settings; ok=false when it has none.
func (s *Store) Load(agentID string) (Config, bool, error) {
	var rows []entity.AgentChannel
	if err := s.db.Where("type = ? AND name = ?", RowType, agentID).Limit(1).Find(&rows).Error; err != nil {
		return Config{}, false, err
	}
	if len(rows) == 0 {
		return Config{}, false, nil
	}
	var c Config
	if err := json.Unmarshal([]byte(rows[0].Config), &c); err != nil {
		return Config{}, false, err
	}
	c.AgentID = agentID
	if rows[0].UserID != nil {
		c.OwnerUserID = *rows[0].UserID
	}
	return c, true, nil
}

// Save upserts c's row.
func (s *Store) Save(c Config) error {
	c.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	now := time.Now()
	res := s.db.Model(&entity.AgentChannel{}).Where("type = ? AND name = ?", RowType, c.AgentID).
		Updates(map[string]any{"config": string(data), "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	owner := c.OwnerUserID
	return s.db.Create(&entity.AgentChannel{
		ID: RowID(c.AgentID), Type: RowType, Name: c.AgentID, UserID: &owner,
		Enabled: true, Config: string(data), CreatedAt: now, UpdatedAt: now,
	}).Error
}

func (s *Store) Delete(agentID string) error {
	return s.db.Where("type = ? AND name = ?", RowType, agentID).Delete(&entity.AgentChannel{}).Error
}

// stateFile holds a session's Slack conversation in its session dir.
const stateFile = "slack-remote.json"

// State is a session's place in Slack: one wick session = one thread.
type State struct {
	Channel   string    `json:"channel,omitempty"`
	ThreadTS  string    `json:"thread_ts,omitempty"`
	LastTS    string    `json:"last_ts,omitempty"`
	LastNote  string    `json:"last_note,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func LoadState(dir string) State {
	var st State
	if dir == "" {
		return st
	}
	if b, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(dir string, st State) {
	if dir == "" {
		return
	}
	st.UpdatedAt = time.Now().UTC()
	b, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(dir, stateFile), b, 0o600); err != nil {
		log.Warn().Err(err).Msg("slackremote: save session state")
	}
}
