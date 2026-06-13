package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Skill struct {
	ID        string  `gorm:"type:varchar(36);primaryKey"`
	Name      string  `gorm:"uniqueIndex;not null"`
	IsSystem  bool    `gorm:"not null;default:false"`
	CreatedBy *string `gorm:"column:created_by;type:varchar(36)"`
	FilePath  string  `gorm:"column:file_path"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Skill) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	return nil
}
