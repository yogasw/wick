package entity

import "time"

// TeamSettings is one user's Team settings: what applies to every agent
// in their Team (package internal/agents/team). One row per user, made on
// the first save; a user without a row has the zero value, which is the
// default everywhere.
type TeamSettings struct {
	UserID string `gorm:"primaryKey;type:varchar(64)" json:"user_id"`
	// Prompt is the owner's "Team instructions": markdown added to the
	// system prompt of each of their Team agents. "" = none.
	Prompt string `gorm:"type:text;not null;default:''" json:"prompt"`
	// ClassicHome turns OFF "Open Team when I open Agents". Stored
	// inverted so the zero value is the default (ON): gorm skips a zero
	// field that has a column default on insert, so a false default-true
	// column could never be saved.
	ClassicHome bool      `gorm:"not null;default:false" json:"classic_home"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// OpenTeam reports whether opening the Agents landing goes to Team.
func (s TeamSettings) OpenTeam() bool { return !s.ClassicHome }
