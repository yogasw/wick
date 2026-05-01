package tags

import (
	"context"
	"github.com/yogasw/wick/internal/entity"

	"gorm.io/gorm"
)

type repo struct {
	db *gorm.DB
}

func newRepo(db *gorm.DB) *repo {
	return &repo{db: db}
}

// ListGroupTags returns tags flagged as groups, ordered by sort_order then name.
func (r *repo) ListGroupTags(ctx context.Context) ([]*entity.Tag, error) {
	var tags []*entity.Tag
	err := r.db.WithContext(ctx).
		Where("is_group = ?", true).
		Order("sort_order asc, name asc").
		Find(&tags).Error
	return tags, err
}

// ListToolTags returns every ToolTag row whose tool_path is in toolPaths.
func (r *repo) ListToolTags(ctx context.Context, toolPaths []string) ([]entity.ToolTag, error) {
	var rows []entity.ToolTag
	if err := r.db.WithContext(ctx).
		Where("tool_path IN ?", toolPaths).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetTagByName returns the tag with the given name, or gorm.ErrRecordNotFound.
func (r *repo) GetTagByName(ctx context.Context, name string) (*entity.Tag, error) {
	var t entity.Tag
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTag inserts a new tag row.
func (r *repo) CreateTag(ctx context.Context, t *entity.Tag) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// HasToolTags reports whether the tool_path already has any tool_tag rows.
func (r *repo) HasToolTags(ctx context.Context, toolPath string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&entity.ToolTag{}).
		Where("tool_path = ?", toolPath).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// TagsByIDs returns the tags whose ids are in ids, in arbitrary order.
// An empty ids slice yields an empty result without hitting the DB.
func (r *repo) TagsByIDs(ctx context.Context, ids []string) ([]*entity.Tag, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var tags []*entity.Tag
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&tags).Error
	return tags, err
}

// LinkToolTag creates a tool_tag row, ignoring duplicates.
func (r *repo) LinkToolTag(ctx context.Context, toolPath, tagID string) error {
	link := entity.ToolTag{ToolPath: toolPath, TagID: tagID}
	return r.db.WithContext(ctx).
		Where("tool_path = ? AND tag_id = ?", toolPath, tagID).
		FirstOrCreate(&link).Error
}

// SyncSystemTagsForAllAdmins ensures every admin user carries every
// IsSystem tag. Idempotent — duplicate (user, tag) pairs are skipped
// via FirstOrCreate. Called from boot to backfill admins that pre-date
// the System-tag schema; per-user role changes after boot use the
// inline sync in admin.Repo.SetRole.
func (r *repo) SyncSystemTagsForAllAdmins(ctx context.Context) error {
	var systemTagIDs []string
	if err := r.db.WithContext(ctx).Model(&entity.Tag{}).
		Where("is_system = ?", true).
		Pluck("id", &systemTagIDs).Error; err != nil {
		return err
	}
	if len(systemTagIDs) == 0 {
		return nil
	}
	var adminIDs []string
	if err := r.db.WithContext(ctx).Model(&entity.User{}).
		Where("role = ?", entity.RoleAdmin).
		Pluck("id", &adminIDs).Error; err != nil {
		return err
	}
	for _, uid := range adminIDs {
		for _, tid := range systemTagIDs {
			link := entity.UserTag{UserID: uid, TagID: tid}
			if err := r.db.WithContext(ctx).
				Where("user_id = ? AND tag_id = ?", uid, tid).
				FirstOrCreate(&link).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
