package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type UserRole string

const (
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

type User struct {
	ID           string `gorm:"type:varchar(36);primaryKey"`
	Email        string `gorm:"uniqueIndex;not null"`
	Name         string `gorm:"not null"`
	Avatar       string
	Role         UserRole     `gorm:"type:varchar(50);default:'user'"`
	Approved     bool         `gorm:"default:false"`
	PasswordHash string       `gorm:"type:varchar(255)"`
	Metadata     UserMetadata `gorm:"type:jsonb"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	return nil
}

func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}
