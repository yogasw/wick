package bookmark

import (
	"context"
	"github.com/yogasw/wick/internal/entity"
	"time"

	"gorm.io/gorm"
)

type repo struct {
	db *gorm.DB
}

func newRepo(db *gorm.DB) *repo {
	return &repo{db: db}
}

// Find returns the bookmark for (userID, toolPath) or gorm.ErrRecordNotFound.
func (r *repo) Find(ctx context.Context, userID, toolPath string) (*entity.Bookmark, error) {
	var b entity.Bookmark
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND tool_path = ?", userID, toolPath).
		First(&b).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

// Delete removes the bookmark for (userID, toolPath).
func (r *repo) Delete(ctx context.Context, userID, toolPath string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND tool_path = ?", userID, toolPath).
		Delete(&entity.Bookmark{}).Error
}

// Create inserts a new bookmark row.
func (r *repo) Create(ctx context.Context, userID, toolPath string) error {
	b := entity.Bookmark{UserID: userID, ToolPath: toolPath, CreatedAt: time.Now()}
	return r.db.WithContext(ctx).Create(&b).Error
}

// ListByUser returns every bookmark belonging to userID.
func (r *repo) ListByUser(ctx context.Context, userID string) ([]entity.Bookmark, error) {
	var rows []entity.Bookmark
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
