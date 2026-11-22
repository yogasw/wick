package sso

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

func (r *repo) ListAll(ctx context.Context) ([]entity.SSOProvider, error) {
	var out []entity.SSOProvider
	err := r.db.WithContext(ctx).Order("provider asc").Find(&out).Error
	return out, err
}

func (r *repo) FindByProvider(ctx context.Context, provider string) (*entity.SSOProvider, error) {
	var p entity.SSOProvider
	err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// EnsureProvider inserts an empty row for the given provider when one
// doesn't exist yet. Used at bootstrap so the admin UI shows a "Google
// (not configured)" row immediately instead of an empty list.
func (r *repo) EnsureProvider(ctx context.Context, provider string) error {
	var existing entity.SSOProvider
	err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return r.db.WithContext(ctx).Create(&entity.SSOProvider{Provider: provider}).Error
}

// Update writes the editable fields on a provider row.
func (r *repo) Update(ctx context.Context, provider string, in UpdateInput) error {
	return r.db.WithContext(ctx).Model(&entity.SSOProvider{}).
		Where("provider = ?", provider).
		Updates(map[string]any{
			"client_id":             in.ClientID,
			"client_secret":         in.ClientSecret,
			"enabled":               in.Enabled,
			"allowed_domains":       in.AllowedDomains,
			"auto_approve_domains":  in.AutoApproveDomains,
			"disable_auto_register": in.DisableAutoRegister,
		}).Error
}
