package configs

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

func (r *repo) ListAll(ctx context.Context) ([]entity.Config, error) {
	var out []entity.Config
	err := r.db.WithContext(ctx).Find(&out).Error
	return out, err
}

// FindByOwnerKey returns one row or gorm.ErrRecordNotFound.
func (r *repo) FindByOwnerKey(ctx context.Context, owner, key string) (*entity.Config, error) {
	var v entity.Config
	err := r.db.WithContext(ctx).Where("owner = ? AND key = ?", owner, key).First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// UpsertMeta inserts a row or, if it exists, refreshes metadata
// columns (type, options, is_secret, can_regenerate, locked,
// required, description) while preserving the stored value. Used at
// bootstrap to keep DB in sync with module declarations.
func (r *repo) UpsertMeta(ctx context.Context, v *entity.Config) error {
	var existing entity.Config
	err := r.db.WithContext(ctx).Where("owner = ? AND key = ?", v.Owner, v.Key).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(v).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&existing).
		Where("owner = ? AND key = ?", v.Owner, v.Key).
		Updates(map[string]any{
			"type":           v.Type,
			"options":        v.Options,
			"is_secret":      v.IsSecret,
			"can_regenerate": v.CanRegenerate,
			"locked":         v.Locked,
			"required":       v.Required,
			"description":    v.Description,
		}).Error
}

func (r *repo) SetValue(ctx context.Context, owner, key, value string) error {
	return r.db.WithContext(ctx).Model(&entity.Config{}).
		Where("owner = ? AND key = ?", owner, key).
		Update("value", value).Error
}
