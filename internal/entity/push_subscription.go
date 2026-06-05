package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PushSubscription is one browser/device endpoint for notifications.
// A single user can have many rows: desktop Chrome, Android Chrome,
// Safari, Firefox, and so on. Endpoint is globally unique because
// vendors issue it per subscription.
type PushSubscription struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	UserID      string `gorm:"type:varchar(36);not null;index"`
	User        User   `gorm:"foreignKey:UserID"`
	Endpoint    string `gorm:"not null;uniqueIndex;type:text"`
	P256dh      string `gorm:"not null;type:text"`
	Auth        string `gorm:"not null;type:text"`
	UserAgent   string `gorm:"type:text"`
	DeviceLabel string `gorm:"type:varchar(160)"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastSeenAt  *time.Time
	DisabledAt  *time.Time `gorm:"index"`
}

func (s *PushSubscription) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	return nil
}
