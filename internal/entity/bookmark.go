package entity

import "time"

// Bookmark marks a tool as a favorite for a specific user. Bookmarked
// tools appear in a dedicated "Bookmarks" group on the home page in
// addition to any group/category they already belong to.
type Bookmark struct {
	UserID    string `gorm:"primaryKey;type:varchar(36)"`
	ToolPath  string `gorm:"primaryKey;type:varchar(255)"`
	CreatedAt time.Time
}
