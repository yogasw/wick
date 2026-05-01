package admin

import (
	"context"
	"errors"
	"github.com/yogasw/wick/internal/entity"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type repo struct {
	db *gorm.DB
}

func newRepo(db *gorm.DB) *repo {
	return &repo{db: db}
}

// ── Users ────────────────────────────────────────────────────

func (r *repo) ListUsers(ctx context.Context) ([]*entity.User, error) {
	var users []*entity.User
	if err := r.db.WithContext(ctx).Order("created_at asc").Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func (r *repo) GetUser(ctx context.Context, userID string) (*entity.User, error) {
	var u entity.User
	if err := r.db.WithContext(ctx).First(&u, "id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *repo) SetApproved(ctx context.Context, userID string, approved bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&entity.User{}).Where("id = ?", userID).Update("approved", approved).Error; err != nil {
			return err
		}
		if !approved {
			// A revoked user must not keep tags — tags are only meaningful
			// for approved users. System tags will be re-synced from role
			// on re-approve, so wiping them here is safe.
			return tx.Where("user_id = ?", userID).Delete(&entity.UserTag{}).Error
		}
		// On approve: re-sync system tags to match current role. Fixes
		// any drift introduced by an earlier revoke that wiped them.
		var u entity.User
		if err := tx.First(&u, "id = ?", userID).Error; err != nil {
			return err
		}
		return syncSystemTagsForRole(tx, userID, u.Role)
	})
}

// ErrLastAdmin is returned when trying to demote the only remaining admin.
var ErrLastAdmin = errors.New("cannot demote the last admin")

func (r *repo) CountAdmins(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&entity.User{}).Where("role = ?", entity.RoleAdmin).Count(&count).Error
	return count, err
}

func (r *repo) SetRole(ctx context.Context, userID string, role entity.UserRole) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if role != entity.RoleAdmin {
			var user entity.User
			if err := tx.First(&user, "id = ?", userID).Error; err != nil {
				return err
			}
			if user.Role == entity.RoleAdmin {
				var count int64
				if err := tx.Model(&entity.User{}).Where("role = ?", entity.RoleAdmin).Count(&count).Error; err != nil {
					return err
				}
				if count <= 1 {
					return ErrLastAdmin
				}
			}
		}
		if err := tx.Model(&entity.User{}).Where("id = ?", userID).Update("role", role).Error; err != nil {
			return err
		}
		return syncSystemTagsForRole(tx, userID, role)
	})
}

// syncSystemTagsForRole keeps a user's System UserTag rows in lockstep
// with their role. Admins carry every IsSystem tag (so they show up in
// /manager/* surfaces gated by tag-filter); non-admins carry none.
//
// Called from SetRole inside the role-change transaction so a partial
// promotion/demotion isn't possible.
func syncSystemTagsForRole(tx *gorm.DB, userID string, role entity.UserRole) error {
	var systemTagIDs []string
	if err := tx.Model(&entity.Tag{}).Where("is_system = ?", true).
		Pluck("id", &systemTagIDs).Error; err != nil {
		return err
	}
	if len(systemTagIDs) == 0 {
		return nil
	}
	if role == entity.RoleAdmin {
		for _, id := range systemTagIDs {
			link := entity.UserTag{UserID: userID, TagID: id}
			if err := tx.Where("user_id = ? AND tag_id = ?", userID, id).
				FirstOrCreate(&link).Error; err != nil {
				return err
			}
		}
		return nil
	}
	return tx.Where("user_id = ? AND tag_id IN ?", userID, systemTagIDs).
		Delete(&entity.UserTag{}).Error
}

// GetUserTagIDs returns the set of tag ids a user currently has.
func (r *repo) GetUserTagIDs(ctx context.Context, userID string) ([]string, error) {
	var rows []entity.UserTag
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, t := range rows {
		ids[i] = t.TagID
	}
	return ids, nil
}

// ErrUserNotApproved is returned when trying to assign tags to an unapproved user.
var ErrUserNotApproved = errors.New("user must be approved before assigning tags")

// ErrSystemTagAssignment is returned when an admin tries to attach a
// tag flagged IsSystem to a user. System tags are code-owned and exist
// solely to gate built-in maintenance items behind a tag no end user
// can carry — see entity.Tag godoc.
var ErrSystemTagAssignment = errors.New("system tags cannot be assigned to users")

func (r *repo) SetUserTags(ctx context.Context, userID string, tagIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u entity.User
		if err := tx.First(&u, "id = ?", userID).Error; err != nil {
			return err
		}
		if !u.Approved && len(tagIDs) > 0 {
			return ErrUserNotApproved
		}
		// Reject the request outright if any of the requested tags is
		// marked IsSystem. We check before mutating so a bad payload
		// doesn't blank the user's tags as a side effect. System tags
		// are managed exclusively by SetRole — this picker never sees
		// them in the UI either (handler strips them).
		cleaned := make([]string, 0, len(tagIDs))
		for _, id := range tagIDs {
			id = strings.TrimSpace(id)
			if id != "" {
				cleaned = append(cleaned, id)
			}
		}
		if len(cleaned) > 0 {
			var systemHits int64
			if err := tx.Model(&entity.Tag{}).
				Where("id IN ? AND is_system = ?", cleaned, true).
				Count(&systemHits).Error; err != nil {
				return err
			}
			if systemHits > 0 {
				return ErrSystemTagAssignment
			}
		}
		// Wipe only non-system UserTag rows. Admin's auto-assigned
		// System tags survive the picker rewrite — they're owned by
		// SetRole, not this handler.
		if err := tx.
			Where(`user_id = ? AND tag_id NOT IN (
				SELECT id FROM tags WHERE is_system = ?
			)`, userID, true).
			Delete(&entity.UserTag{}).Error; err != nil {
			return err
		}
		for _, id := range cleaned {
			if err := tx.Create(&entity.UserTag{UserID: userID, TagID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ── Tool permissions ─────────────────────────────────────────

type ToolPerm struct {
	Path       string
	Visibility entity.ToolVisibility
	Disabled   bool
	TagIDs     []string
}

func (r *repo) ListToolPerms(ctx context.Context, toolPaths []string) ([]*ToolPerm, error) {
	var perms []entity.ToolPermission
	r.db.WithContext(ctx).Where("tool_path IN ?", toolPaths).Find(&perms)
	permMap := make(map[string]entity.ToolPermission, len(perms))
	for _, p := range perms {
		permMap[p.ToolPath] = p
	}

	var toolTags []entity.ToolTag
	r.db.WithContext(ctx).Where("tool_path IN ?", toolPaths).Find(&toolTags)
	tagMap := make(map[string][]string)
	for _, t := range toolTags {
		tagMap[t.ToolPath] = append(tagMap[t.ToolPath], t.TagID)
	}

	result := make([]*ToolPerm, len(toolPaths))
	for i, path := range toolPaths {
		p, ok := permMap[path]
		vis := p.Visibility
		if !ok {
			vis = entity.VisibilityPrivate
		}
		result[i] = &ToolPerm{Path: path, Visibility: vis, Disabled: p.Disabled, TagIDs: tagMap[path]}
	}
	return result, nil
}

func (r *repo) SetToolVisibility(ctx context.Context, toolPath string, vis entity.ToolVisibility) error {
	perm := entity.ToolPermission{ToolPath: toolPath, Visibility: vis, UpdatedAt: time.Now()}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tool_path"}},
		DoUpdates: clause.AssignmentColumns([]string{"visibility", "updated_at"}),
	}).Create(&perm).Error
}

func (r *repo) SetToolDisabled(ctx context.Context, toolPath string, disabled bool) error {
	perm := entity.ToolPermission{
		ToolPath:   toolPath,
		Visibility: entity.VisibilityPrivate,
		Disabled:   disabled,
		UpdatedAt:  time.Now(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tool_path"}},
		DoUpdates: clause.AssignmentColumns([]string{"disabled", "updated_at"}),
	}).Create(&perm).Error
}

func (r *repo) SetToolTags(ctx context.Context, toolPath string, tagIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tool_path = ?", toolPath).Delete(&entity.ToolTag{}).Error; err != nil {
			return err
		}
		for _, id := range tagIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if err := tx.Create(&entity.ToolTag{ToolPath: toolPath, TagID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ── Tags ─────────────────────────────────────────────────────

func (r *repo) ListTags(ctx context.Context) ([]*entity.Tag, error) {
	var tags []*entity.Tag
	if err := r.db.WithContext(ctx).Order("sort_order asc, name asc").Find(&tags).Error; err != nil {
		return nil, err
	}
	return tags, nil
}

// ErrTagNameTaken is returned when creating or renaming a tag to an existing name.
var ErrTagNameTaken = errors.New("a tag with that name already exists")

// ErrSystemTagImmutable is returned when an admin tries to edit, delete,
// or otherwise mutate a Tag whose IsSystem flag is true. System tags are
// owned by code (seeded via tool/job DefaultTags) and changing them from
// the UI would desync the seed catalog from the DB.
var ErrSystemTagImmutable = errors.New("system tags are read-only and cannot be modified")

func (r *repo) CreateTag(ctx context.Context, name string, isGroup, isFilter bool) (*entity.Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("tag name is required")
	}
	var existing entity.Tag
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&existing).Error
	if err == nil {
		return nil, ErrTagNameTaken
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	t := entity.Tag{Name: name, IsGroup: isGroup, IsFilter: isFilter}
	if err := r.db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTag renames and/or updates metadata (description, is_group,
// sort_order) in a single call. Pass the full desired state. Refuses to
// touch tags flagged IsSystem — those are code-owned.
func (r *repo) UpdateTag(ctx context.Context, tagID, name, description string, isGroup, isFilter bool, sortOrder int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tag name is required")
	}
	var current entity.Tag
	if err := r.db.WithContext(ctx).First(&current, "id = ?", tagID).Error; err != nil {
		return err
	}
	if current.IsSystem {
		return ErrSystemTagImmutable
	}
	var clash entity.Tag
	err := r.db.WithContext(ctx).Where("name = ? AND id <> ?", name, tagID).First(&clash).Error
	if err == nil {
		return ErrTagNameTaken
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return r.db.WithContext(ctx).Model(&entity.Tag{}).
		Where("id = ?", tagID).
		Updates(map[string]any{
			"name":        name,
			"description": strings.TrimSpace(description),
			"is_group":    isGroup,
			"is_filter":   isFilter,
			"sort_order":  sortOrder,
		}).Error
}

// DeleteTag removes a tag and all its associations (tool_tags, user_tags).
// Refuses to delete tags flagged IsSystem — they are seeded from code at
// every boot anyway, so deletion would resurrect them empty.
func (r *repo) DeleteTag(ctx context.Context, tagID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current entity.Tag
		if err := tx.First(&current, "id = ?", tagID).Error; err != nil {
			return err
		}
		if current.IsSystem {
			return ErrSystemTagImmutable
		}
		if err := tx.Where("tag_id = ?", tagID).Delete(&entity.ToolTag{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tag_id = ?", tagID).Delete(&entity.UserTag{}).Error; err != nil {
			return err
		}
		return tx.Delete(&entity.Tag{}, "id = ?", tagID).Error
	})
}
