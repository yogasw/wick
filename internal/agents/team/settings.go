package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	// UpdateAll: a column added to entity.TeamSettings is saved without
	// touching this list.
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		UpdateAll: true,
	}).Create(st).Error
}

/* ── setting registry ────────────────────────────────────────────────── */

// SettingField is one user-editable Team setting as GET/PUT
// /api/team/settings sees it. Team settings are the home of every
// per-user Team option, so adding one is: a typed column on
// entity.TeamSettings, plus one entry in SettingFields (wire key, how to
// read it, how to validate and set it). The API, the upsert and the
// unknown-key check follow from the registry.
type SettingField struct {
	// Key is the JSON name on the wire.
	Key string
	Get func(entity.TeamSettings) any
	// Set decodes and validates raw into st; an error leaves the save
	// refused (ApplySettings works on a copy).
	Set func(st *entity.TeamSettings, raw json.RawMessage) error
}

// SettingFields lists every Team setting, in display order.
var SettingFields = []SettingField{
	{
		Key: "prompt",
		Get: func(st entity.TeamSettings) any { return st.Prompt },
		Set: func(st *entity.TeamSettings, raw json.RawMessage) error {
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return errors.New("prompt must be a string")
			}
			if err := ValidateTeamPrompt(v); err != nil {
				return err
			}
			st.Prompt = v
			return nil
		},
	},
	{
		Key: "open_team",
		Get: func(st entity.TeamSettings) any { return st.OpenTeam() },
		Set: func(st *entity.TeamSettings, raw json.RawMessage) error {
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				return errors.New("open_team must be true or false")
			}
			st.TeamHome = v
			return nil
		},
	},
	{
		Key: "idle_animations",
		Get: func(st entity.TeamSettings) any { return st.IdleAnimations() },
		Set: func(st *entity.TeamSettings, raw json.RawMessage) error {
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				return errors.New("idle_animations must be true or false")
			}
			st.NoIdleFidget = !v
			return nil
		},
	},
}

// SettingValues is st as the wire map: one entry per SettingFields key.
func SettingValues(st entity.TeamSettings) map[string]any {
	out := make(map[string]any, len(SettingFields))
	for _, f := range SettingFields {
		out[f.Key] = f.Get(st)
	}
	return out
}

// ApplySettings sets the fields patch names on st. All or nothing: an
// unknown key or one invalid value leaves st unchanged and names every
// problem.
func ApplySettings(st *entity.TeamSettings, patch map[string]json.RawMessage) error {
	byKey := make(map[string]SettingField, len(SettingFields))
	for _, f := range SettingFields {
		byKey[f.Key] = f
	}
	next := *st
	var problems []string
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, ok := byKey[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("unknown setting %q", k))
			continue
		}
		if err := f.Set(&next, patch[k]); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	*st = next
	return nil
}
