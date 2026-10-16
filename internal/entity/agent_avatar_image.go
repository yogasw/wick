package entity

import "time"

// AgentAvatarImage is the picture behind an agent's "image" avatar
// (team.AvatarKindImage): one row per agent, the bytes the server
// re-encoded itself (internal/agents/team/avatar_image.go), never the
// file as uploaded. It lives in the database, not on disk, so it follows
// the agent across hosts and is gone with it.
type AgentAvatarImage struct {
	AgentID string `gorm:"primaryKey;type:varchar(64)" json:"agent_id"`
	// ContentType is image/png or image/jpeg, set by the server.
	ContentType string `gorm:"type:varchar(32);not null" json:"content_type"`
	// Version is a hash of Data: the ?v= cache key and the ETag.
	Version   string    `gorm:"type:varchar(32);not null" json:"version"`
	Width     int       `gorm:"not null" json:"width"`
	Height    int       `gorm:"not null" json:"height"`
	Data      []byte    `gorm:"not null" json:"-"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}
