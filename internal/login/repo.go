package login

import (
	"context"
	"errors"
	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
)

type repo struct {
	db *gorm.DB
}

func newRepo(db *gorm.DB) *repo {
	return &repo{db: db}
}

func (r *repo) UpsertUser(ctx context.Context, email, name, avatar string, adminEmails map[string]bool) (*entity.User, error) {
	var u entity.User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		role := entity.RoleUser
		approved := false
		if adminEmails[email] {
			role = entity.RoleAdmin
			approved = true
		}
		u = entity.User{Email: email, Name: name, Avatar: avatar, Role: role, Approved: approved}
		if err := r.db.WithContext(ctx).Create(&u).Error; err != nil {
			return nil, err
		}
		return &u, nil
	}
	if err != nil {
		return nil, err
	}

	updates := map[string]any{"name": name, "avatar": avatar}
	if adminEmails[email] && u.Role != entity.RoleAdmin {
		updates["role"] = entity.RoleAdmin
		updates["approved"] = true
	}
	if err := r.db.WithContext(ctx).Model(&u).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &u, nil
}


func (r *repo) GetUserByEmail(ctx context.Context, email string) (*entity.User, error) {
	var u entity.User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *repo) GetUserByID(ctx context.Context, id string) (*entity.User, error) {
	var u entity.User
	if err := r.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *repo) SetMetadata(ctx context.Context, userID string, meta entity.UserMetadata) error {
	return r.db.WithContext(ctx).Model(&entity.User{}).
		Where("id = ?", userID).
		Update("metadata", meta).Error
}

func (r *repo) SetPasswordHash(ctx context.Context, userID, hash string) error {
	return r.db.WithContext(ctx).Model(&entity.User{}).
		Where("id = ?", userID).
		Update("password_hash", hash).Error
}

// CountAdmins returns how many users carry the admin role. Used by
// the first-boot admin seed to decide whether the default admin
// should be created.
func (r *repo) CountAdmins(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&entity.User{}).
		Where("role = ?", entity.RoleAdmin).
		Count(&n).Error
	return n, err
}

// GetToolPerm returns the per-tool permission row, falling back to
// (fallback, false) when no row exists.
func (r *repo) GetToolPerm(ctx context.Context, toolPath string, fallback entity.ToolVisibility) (vis entity.ToolVisibility, disabled bool) {
	var p entity.ToolPermission
	if err := r.db.WithContext(ctx).First(&p, "tool_path = ?", toolPath).Error; err != nil {
		return fallback, false
	}
	return p.Visibility, p.Disabled
}

// GetUserFilterTagIDs returns the tag IDs of all filter-type tags assigned to a user.
// Called once at login and embedded in the encrypted session cookie.
func (r *repo) GetUserFilterTagIDs(ctx context.Context, userID string) []string {
	var ids []string
	r.db.WithContext(ctx).
		Table("user_tags").
		Joins("JOIN tags ON tags.id = user_tags.tag_id").
		Where("user_tags.user_id = ? AND tags.is_filter = ?", userID, true).
		Pluck("user_tags.tag_id", &ids)
	return ids
}

// GetToolFilterTagIDs returns the tag IDs that restrict access to a tool.
// An empty result means the tool has no tag restriction.
func (r *repo) GetToolFilterTagIDs(ctx context.Context, toolPath string) []string {
	var ids []string
	r.db.WithContext(ctx).
		Table("tool_tags").
		Joins("JOIN tags ON tags.id = tool_tags.tag_id").
		Where("tool_tags.tool_path = ? AND tags.is_filter = ?", toolPath, true).
		Pluck("tool_tags.tag_id", &ids)
	return ids
}
