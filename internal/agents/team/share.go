package team

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// Sharing: an owner hands one agent to another wick user, chat only. The
// share row is all there is — the recipient never gets a copy of the
// agent, so an edit, a disable or a delete reaches them at once.

// An admin can also share an agent with everyone holding a tag (Admin ›
// Team agents, tool_tags path TagSharePath). A tag share has no row of its
// own: it lasts exactly as long as the tag is on the agent and the
// recipient holds it. The only row it may leave behind is the recipient's
// read mark, stamped ShareByTags so it never counts as a share itself.

// ShareByTags is the CreatedBy of a row that only carries a tag
// recipient's read mark. It grants nothing on its own.
const ShareByTags = "tags"

// TagSharePath is the tool_tags path holding the tags agentID is shared by.
func TagSharePath(agentID string) string { return tagSharePrefix + agentID }

const tagSharePrefix = "/team-agents/"

// explicitShare filters to the rows an owner or admin created by hand.
const explicitShare = "created_by <> '" + ShareByTags + "'"

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
// existing row is kept, so the first share's date stays. A tag recipient's
// read-mark row becomes a real share, keeping its read mark.
func (s *Store) AddShare(ctx context.Context, agentID, userID, by string) (err error) {
	done := s.changing(ctx, agentID)
	defer func() { done(err) }()
	if err := s.db.WithContext(ctx).Model(&entity.AgentShare{}).
		Where("agent_id = ? AND shared_with_user_id = ? AND created_by = ?", agentID, userID, ShareByTags).
		Updates(map[string]any{"created_by": by, "created_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	row := entity.AgentShare{
		ID: uuid.NewString(), AgentID: agentID, SharedWithUserID: userID,
		CreatedBy: by, CreatedAt: time.Now().UTC(),
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// RemoveShare drops one share; a missing one is ErrNotFound.
func (s *Store) RemoveShare(ctx context.Context, agentID, userID string) (err error) {
	done := s.changing(ctx, agentID)
	defer func() { done(err) }()
	res := s.db.WithContext(ctx).
		Where("agent_id = ? AND shared_with_user_id = ?", agentID, userID).
		Where(explicitShare).
		Delete(&entity.AgentShare{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteShares drops every share of agentID, its share tags included (the
// agent is being deleted).
func (s *Store) DeleteShares(ctx context.Context, agentID string) (err error) {
	done := s.changing(ctx, agentID)
	defer func() { done(err) }()
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&entity.AgentShare{}).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Where("tool_path = ?", TagSharePath(agentID)).Delete(&entity.ToolTag{}).Error
}

// ListShares returns who agentID is shared with by hand, oldest share
// first. Tag recipients are not listed: the tags say who they are.
func (s *Store) ListShares(ctx context.Context, agentID string) ([]entity.AgentShare, error) {
	var rows []entity.AgentShare
	err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Where(explicitShare).
		Order("created_at ASC").Order("id ASC").Find(&rows).Error
	return rows, err
}

// ShareOf returns agentID's share with userID, ErrNotFound when it has
// none. A tag share answers with its read-mark row (CreatedBy ShareByTags),
// or an unsaved one when the recipient has not opened it yet.
func (s *Store) ShareOf(ctx context.Context, agentID, userID string) (entity.AgentShare, error) {
	var row entity.AgentShare
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND shared_with_user_id = ?", agentID, userID).
		First(&row).Error
	found := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return row, err
	}
	if found && row.CreatedBy != ShareByTags {
		return row, nil
	}
	ids, err := s.tagSharedIDs(ctx, userID, agentID)
	if err != nil {
		return row, err
	}
	if len(ids) == 0 {
		return entity.AgentShare{}, ErrNotFound
	}
	if !found {
		row = tagShareRow(agentID, userID)
	}
	return row, nil
}

// tagShareRow is the unsaved share a tag recipient has before their first
// read mark.
func tagShareRow(agentID, userID string) entity.AgentShare {
	return entity.AgentShare{AgentID: agentID, SharedWithUserID: userID, CreatedBy: ShareByTags}
}

// tagSharedIDs returns the agents shared with userID through a filter tag
// they hold, narrowed to only when given. Same tag rule as
// login.CanAccessSharedResource: a filter tag on the path that the user
// carries directly.
func (s *Store) tagSharedIDs(ctx context.Context, userID string, only ...string) ([]string, error) {
	if userID == "" {
		return nil, nil
	}
	q := s.db.WithContext(ctx).Table("tool_tags").
		Joins("JOIN tags ON tags.id = tool_tags.tag_id").
		Joins("JOIN user_tags ON user_tags.tag_id = tool_tags.tag_id").
		Where("user_tags.user_id = ? AND tags.is_filter = ?", userID, true)
	if len(only) > 0 {
		paths := make([]string, len(only))
		for i, id := range only {
			paths[i] = TagSharePath(id)
		}
		q = q.Where("tool_tags.tool_path IN ?", paths)
	} else {
		q = q.Where("tool_tags.tool_path LIKE ?", tagSharePrefix+"%")
	}
	var paths []string
	if err := q.Distinct().Pluck("tool_tags.tool_path", &paths).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(paths))
	for _, p := range paths {
		if id := strings.TrimPrefix(p, tagSharePrefix); id != "" && !strings.Contains(id, "/") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// MarkShareRead stamps the recipient's own read mark.
func (s *Store) MarkShareRead(ctx context.Context, agentID, userID string, t time.Time) error {
	res := s.db.WithContext(ctx).Model(&entity.AgentShare{}).
		Where("agent_id = ? AND shared_with_user_id = ?", agentID, userID).
		UpdateColumn("last_read_at", t.UTC())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	// A tag recipient's first read mark: the row that carries it.
	ids, err := s.tagSharedIDs(ctx, userID, agentID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return ErrNotFound
	}
	at := t.UTC()
	row := tagShareRow(agentID, userID)
	row.ID, row.CreatedAt, row.LastReadAt = uuid.NewString(), at, &at
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// SharedWith returns the agents shared with userID — another owner's,
// enabled — each with its share row: hand shares in share order, then tag
// shares. Whether one may still be shared (ShareBlock) is the caller's to
// check.
func (s *Store) SharedWith(ctx context.Context, userID string) ([]entity.AgentPersona, []entity.AgentShare, error) {
	var rows []entity.AgentShare
	if err := s.db.WithContext(ctx).Where("shared_with_user_id = ?", userID).
		Order("created_at ASC").Order("id ASC").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	tagIDs, err := s.tagSharedIDs(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	var shares []entity.AgentShare
	marks := map[string]entity.AgentShare{}
	for _, sh := range rows {
		if sh.CreatedBy == ShareByTags {
			marks[sh.AgentID] = sh
			continue
		}
		shares = append(shares, sh)
	}
	seen := make(map[string]bool, len(shares))
	for _, sh := range shares {
		seen[sh.AgentID] = true
	}
	for _, id := range tagIDs {
		if seen[id] {
			continue
		}
		sh, ok := marks[id]
		if !ok {
			sh = tagShareRow(id, userID)
		}
		shares = append(shares, sh)
	}
	if len(shares) == 0 {
		return nil, nil, nil
	}
	ids := make([]string, 0, len(shares))
	for _, sh := range shares {
		ids = append(ids, sh.AgentID)
	}
	var personas []entity.AgentPersona
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&personas).Error; err != nil {
		return nil, nil, err
	}
	byID := make(map[string]entity.AgentPersona, len(personas))
	for _, r := range personas {
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
