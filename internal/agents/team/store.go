package team

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// ErrNotFound is returned when no agent matches.
var ErrNotFound = errors.New("agent not found")

// ErrHandleTaken is returned when the owner already has an agent with the
// handle being saved.
var ErrHandleTaken = errors.New("handle already used by another agent")

// handleRe is the @mention shape: lowercase-kebab, 2–31 runes, starting
// with a letter or digit so "@-x" never parses as a mention.
var handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}$`)

// reservedHandles are mention words that address a group, not an agent.
var reservedHandles = map[string]bool{"all": true, "here": true, "channel": true}

// NormalizeHandle trims and lowercases a typed handle and drops a leading
// "@", so "@Captain " and "captain" save as the same thing.
func NormalizeHandle(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "@")
	return strings.ToLower(h)
}

// ValidateHandle reports why h cannot be an agent handle, or nil.
func ValidateHandle(h string) error {
	if !handleRe.MatchString(h) {
		return fmt.Errorf("handle %q must be 2-31 chars of a-z, 0-9 or '-', starting with a letter or digit", h)
	}
	if reservedHandles[h] {
		return fmt.Errorf("handle %q is reserved", h)
	}
	return nil
}

// Store is the gorm-backed CRUD over agent_personas.
type Store struct {
	db *gorm.DB
}

// NewStore wraps db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// List returns the owner's agents, Captain first, then oldest first so
// the roster order is stable across reloads.
func (s *Store) List(ctx context.Context, ownerID string) ([]entity.AgentPersona, error) {
	var rows []entity.AgentPersona
	err := s.db.WithContext(ctx).
		Where("owner_user_id = ?", ownerID).
		Order("is_captain DESC").Order("created_at ASC").Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// Get returns one agent by id.
func (s *Store) Get(ctx context.Context, id string) (entity.AgentPersona, error) {
	var row entity.AgentPersona
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrNotFound
	}
	return row, err
}

// GetByHandle returns the owner's agent with handle.
func (s *Store) GetByHandle(ctx context.Context, ownerID, handle string) (entity.AgentPersona, error) {
	var row entity.AgentPersona
	err := s.db.WithContext(ctx).
		Where("owner_user_id = ? AND handle = ?", ownerID, NormalizeHandle(handle)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrNotFound
	}
	return row, err
}

// Create inserts p, assigning an id when it has none. See save for the
// rules both write paths share.
func (s *Store) Create(ctx context.Context, p *entity.AgentPersona) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return s.save(ctx, p, true)
}

// Update rewrites every column of an existing agent.
func (s *Store) Update(ctx context.Context, p *entity.AgentPersona) error {
	if p.ID == "" {
		return ErrNotFound
	}
	return s.save(ctx, p, false)
}

// save validates p and writes it in one transaction with the Captain
// rule: at most one Captain per owner, so saving one unsets the rest.
// Doing both in the same transaction is what keeps two concurrent saves
// from each leaving their own Captain behind.
func (s *Store) save(ctx context.Context, p *entity.AgentPersona, create bool) error {
	p.Handle = NormalizeHandle(p.Handle)
	if err := ValidateHandle(p.Handle); err != nil {
		return err
	}
	if p.OwnerUserID == "" {
		return errors.New("agent owner is required")
	}
	if p.AllowedConnectors == "" {
		p.AllowedConnectors = "[]"
	}
	if p.Features == "" {
		p.Features = EncodeFeatures(DefaultFeatures())
	}
	if p.Avatar == "" {
		p.Avatar = EncodeAvatar(DefaultAvatar())
	}
	if create && p.AllowedNativeTools == "" {
		// A new agent starts default-deny on the risky tools; an existing
		// row keeps "" (every tool), see DecodeNativeTools.
		p.AllowedNativeTools = EncodeNativeTools(DefaultNativeTools)
	}
	if p.BashRules == "" {
		p.BashRules = "[]"
	}
	if p.DisabledSkills == "" {
		p.DisabledSkills = "[]"
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Checked up front rather than left to the unique index so the
		// caller gets a sentence instead of a driver error, and the same
		// sentence on postgres and sqlite.
		var clash int64
		if err := tx.Model(&entity.AgentPersona{}).
			Where("owner_user_id = ? AND handle = ? AND id <> ?", p.OwnerUserID, p.Handle, p.ID).
			Count(&clash).Error; err != nil {
			return err
		}
		if clash > 0 {
			return ErrHandleTaken
		}
		if p.IsCaptain {
			if err := tx.Model(&entity.AgentPersona{}).
				Where("owner_user_id = ? AND id <> ? AND is_captain = ?", p.OwnerUserID, p.ID, true).
				Update("is_captain", false).Error; err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		p.UpdatedAt = now
		if create {
			p.CreatedAt = now
			return tx.Create(p).Error
		}
		res := tx.Model(&entity.AgentPersona{}).Where("id = ?", p.ID).Select("*").Omit("created_at", "last_read_at").Updates(p)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// MarkRead stamps the agent's read mark at t. The column alone is written,
// so UpdatedAt is left as the last settings change.
func (s *Store) MarkRead(ctx context.Context, id string, t time.Time) error {
	res := s.db.WithContext(ctx).Model(&entity.AgentPersona{}).Where("id = ?", id).UpdateColumn("last_read_at", t.UTC())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByProjects returns every agent, of any owner, on one of the given
// projects: the people a persona edit on that project reaches.
func (s *Store) ListByProjects(ctx context.Context, projectIDs []string) ([]entity.AgentPersona, error) {
	var rows []entity.AgentPersona
	if len(projectIDs) == 0 {
		return rows, nil
	}
	err := s.db.WithContext(ctx).
		Select("id", "project_id").
		Where("project_id IN ?", projectIDs).
		Find(&rows).Error
	return rows, err
}

// Delete removes the row. The project and sessions it pointed at stay:
// they are the owner's work, not the agent's.
func (s *Store) Delete(ctx context.Context, id string) error {
	res := s.db.WithContext(ctx).Where("id = ?", id).Delete(&entity.AgentPersona{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
