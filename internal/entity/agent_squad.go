package entity

import "time"

// AgentSquad is a named, fixed team: one leader profile plus a roster of
// member profiles it may delegate to.
//
// Without a squad, a leader can reach every profile its caller's tags
// allow, which is fine for ad-hoc work but vague for a recurring job
// ("the release crew"). A squad narrows that to a deliberate line-up, so
// the same task always runs against the same roles.
//
// A squad only ever NARROWS. Membership never grants access the calling
// human lacks — the tag intersection still applies to every member.
type AgentSquad struct {
	ID          string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	Key         string `gorm:"type:varchar(128);uniqueIndex;not null" json:"key"`
	Name        string `gorm:"type:varchar(256);not null;default:''" json:"name"`
	Description string `gorm:"type:text;not null;default:''" json:"description"`
	// LeaderProfileKey is the role that orchestrates. Empty = any profile
	// with can_delegate may lead this squad.
	LeaderProfileKey string `gorm:"type:varchar(128);not null;default:''" json:"leader_profile_key"`
	// MemberProfileKeys is a JSON array of profile keys the leader may
	// delegate to while this squad is active.
	MemberProfileKeys string `gorm:"type:text;not null;default:'[]'" json:"member_profile_keys"`
	Disabled          bool   `gorm:"not null;default:false" json:"disabled"`
	CreatedBy         string `gorm:"type:varchar(128);not null;default:''" json:"created_by"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (AgentSquad) TableName() string { return "agent_squads" }
