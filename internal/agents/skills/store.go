package skills

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Store persists skill ownership records.
type Store struct{ db *gorm.DB }

// NewStore returns a Store backed by db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// FindByName returns the skill row for name, or an error (including gorm.ErrRecordNotFound).
func (s *Store) FindByName(ctx context.Context, name string) (*entity.Skill, error) {
	var skill entity.Skill
	if err := s.db.WithContext(ctx).Where("name = ?", name).First(&skill).Error; err != nil {
		return nil, err
	}
	return &skill, nil
}

// Register upserts a user-owned skill record.
func (s *Store) Register(ctx context.Context, name, createdBy, filePath string) error {
	existing := &entity.Skill{}
	err := s.db.WithContext(ctx).Where("name = ?", name).First(existing).Error
	if err == nil {
		existing.FilePath = filePath
		existing.CreatedBy = &createdBy
		return s.db.WithContext(ctx).Save(existing).Error
	}
	return s.db.WithContext(ctx).Create(&entity.Skill{
		Name:      name,
		CreatedBy: &createdBy,
		FilePath:  filePath,
	}).Error
}

// OwnsSkill reports whether userID is the recorded creator of the named skill.
func (s *Store) OwnsSkill(ctx context.Context, userID, name string) (bool, error) {
	var skill entity.Skill
	err := s.db.WithContext(ctx).Where("name = ? AND created_by = ?", name, userID).First(&skill).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

// RegisterSystem upserts a system-owned skill record (no creator).
func (s *Store) RegisterSystem(ctx context.Context, name, filePath string) error {
	existing := &entity.Skill{}
	err := s.db.WithContext(ctx).Where("name = ?", name).First(existing).Error
	if err == nil {
		existing.FilePath = filePath
		return s.db.WithContext(ctx).Save(existing).Error
	}
	return s.db.WithContext(ctx).Create(&entity.Skill{
		Name:     name,
		IsSystem: true,
		FilePath: filePath,
	}).Error
}
