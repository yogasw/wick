package entity

import "time"

type AgentChannel struct {
	ID        string    `gorm:"primaryKey;type:varchar(64)"`
	Type      string    `gorm:"type:varchar(32);not null;index"`
	Name      string    `gorm:"type:varchar(128);not null;default:'default'"`
	UserID    *string   `gorm:"type:varchar(36);index"`
	Enabled   bool      `gorm:"not null;default:true"`
	Config    string    `gorm:"type:text;not null;default:'{}'"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (AgentChannel) TableName() string { return "agent_channels" }
