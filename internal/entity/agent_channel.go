package entity

import "time"

// AgentChannel is one configured channel instance (Slack, Telegram, etc.).
// Config holds a JSON blob of channel-specific credentials and settings.
// Multiple instances of the same type can coexist (e.g., two Slack bots
// for different teams) distinguished by Name.
type AgentChannel struct {
	ID        string    `gorm:"primaryKey;type:varchar(64)"`
	Type      string    `gorm:"type:varchar(32);not null;index"` // "slack", "telegram"
	Name      string    `gorm:"type:varchar(128);not null;default:'default'"`
	Enabled   bool      `gorm:"not null;default:true"`
	Config    string    `gorm:"type:text;not null;default:'{}'"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (AgentChannel) TableName() string { return "agent_channels" }
