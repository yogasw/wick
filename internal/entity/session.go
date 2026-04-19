package entity

import "time"

type Session struct {
	Token     string    `gorm:"primaryKey;type:varchar(64)"`
	UserID    string    `gorm:"type:uuid;not null;index"`
	User      User      `gorm:"foreignKey:UserID"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}
