package entity

import "time"

// TeamFolders is one user's Team-sidebar folders: the folders they made,
// in their order, and which folder each agent sits in. Per viewer, so a
// recipient files a shared agent where they like without touching the
// owner's own folders. Data is a JSON team.Folders. Its own table rather
// than a column on TeamSettings, so adding it changed no existing row
// shape (a new column invalidates every pooled connection's plans).
type TeamFolders struct {
	UserID    string    `gorm:"primaryKey;type:varchar(64)" json:"user_id"`
	Data      string    `gorm:"type:text;not null;default:'{}'" json:"data"`
	UpdatedAt time.Time `json:"updated_at"`
}
