package team

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// MaxTeamPromptBytes caps the Team instructions. They ride in every turn
// of every agent the owner has, so the cap is about prompt cost, not
// storage.
const MaxTeamPromptBytes = 16 * 1024

// ValidateTeamPrompt reports why s cannot be saved as Team instructions.
func ValidateTeamPrompt(s string) error {
	if len(s) > MaxTeamPromptBytes {
		return fmt.Errorf("team prompt is %d bytes, the limit is %d", len(s), MaxTeamPromptBytes)
	}
	return nil
}

// Settings returns userID's Team settings; a user who never saved any
// gets the zero value (the defaults).
func (s *Store) Settings(ctx context.Context, userID string) (entity.TeamSettings, error) {
	row := entity.TeamSettings{UserID: userID}
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.TeamSettings{UserID: userID}, nil
	}
	return row, err
}

// SaveSettings writes the whole row of st.UserID, creating it on the first
// save.
func (s *Store) SaveSettings(ctx context.Context, st *entity.TeamSettings) error {
	if st.UserID == "" {
		return errors.New("team settings: no user")
	}
	if err := ValidateTeamPrompt(st.Prompt); err != nil {
		return err
	}
	st.UpdatedAt = time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"prompt", "classic_home", "updated_at"}),
	}).Create(st).Error
}
