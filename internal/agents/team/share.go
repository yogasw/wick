package team

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// Sharing: an owner hands one agent to another wick user, chat only. The
// share row is all there is — the recipient never gets a copy of the
// agent, so an edit, a disable or a delete reaches them at once.

// ErrShareSelf refuses sharing an agent with its own owner.
var ErrShareSelf = errors.New("the agent is already yours")

// ShareBlock says why p can never be shared, "" when it can. remoteOwnerOnly
// is the remote agent's usage check (usage "only_me"), which lives with the
// remote stores outside this package.
func ShareBlock(p entity.AgentPersona, remoteOwnerOnly bool) string {
	switch {
	case p.IsCaptain:
		return "The Captain runs your Team and cannot be shared."
	case remoteOwnerOnly:
		return `This remote agent is set to "Only me" — allow agents and people in its Usage setting before sharing it.`
	}
	return ""
}

// AddShare shares agentID with userID. Sharing twice is not an error: the
// existing row is kept, so the first share's date stays.
func (s *Store) AddShare(ctx context.Context, agentID, userID, by string) error {
	row := entity.AgentShare{
		ID: uuid.NewString(), AgentID: agentID, SharedWithUserID: userID,
		CreatedBy: by, CreatedAt: time.Now().UTC(),
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// RemoveShare drops one share; a missing one is ErrNotFound.
func (s *Store) RemoveShare(ctx context.Context, agentID, userID string) error {
	res := s.db.WithContext(ctx).
		Where("agent_id = ? AND shared_with_user_id = ?", agentID, userID).
		Delete(&entity.AgentShare{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteShares drops every share of agentID (the agent is being deleted).
func (s *Store) DeleteShares(ctx context.Context, agentID string) error {
	return s.db.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&entity.AgentShare{}).Error
}

// ListShares returns who agentID is shared with, oldest share first.
func (s *Store) ListShares(ctx context.Context, agentID string) ([]entity.AgentShare, error) {
	var rows []entity.AgentShare
	err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).
		Order("created_at ASC").Order("id ASC").Find(&rows).Error
	return rows, err
}

// ShareOf returns agentID's share with userID, ErrNotFound when it has none.
func (s *Store) ShareOf(ctx context.Context, agentID, userID string) (entity.AgentShare, error) {
	var row entity.AgentShare
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND shared_with_user_id = ?", agentID, userID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrNotFound
	}
	return row, err
}

// MarkShareRead stamps the recipient's own read mark.
func (s *Store) MarkShareRead(ctx context.Context, agentID, userID string, t time.Time) error {
	res := s.db.WithContext(ctx).Model(&entity.AgentShare{}).
		Where("agent_id = ? AND shared_with_user_id = ?", agentID, userID).
		UpdateColumn("last_read_at", t.UTC())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// SharedWith returns the agents shared with userID — another owner's,
// enabled — each with its share row, in share order. Whether one may
// still be shared (ShareBlock) is the caller's to check.
func (s *Store) SharedWith(ctx context.Context, userID string) ([]entity.AgentPersona, []entity.AgentShare, error) {
	var shares []entity.AgentShare
	if err := s.db.WithContext(ctx).Where("shared_with_user_id = ?", userID).
		Order("created_at ASC").Order("id ASC").Find(&shares).Error; err != nil {
		return nil, nil, err
	}
	if len(shares) == 0 {
		return nil, nil, nil
	}
	ids := make([]string, 0, len(shares))
	for _, sh := range shares {
		ids = append(ids, sh.AgentID)
	}
	var rows []entity.AgentPersona
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	byID := make(map[string]entity.AgentPersona, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	var agents []entity.AgentPersona
	var kept []entity.AgentShare
	for _, sh := range shares {
		p, ok := byID[sh.AgentID]
		if !ok || p.Disabled || p.OwnerUserID == userID {
			continue
		}
		agents, kept = append(agents, p), append(kept, sh)
	}
	return agents, kept, nil
}
