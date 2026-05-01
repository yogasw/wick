package accesstoken

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Repo wraps the gorm handle. All queries scope on context so HTTP
// cancellation propagates into the DB driver.
type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Create inserts a new token row.
func (r *Repo) Create(ctx context.Context, t *entity.PersonalAccessToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// ListActiveByUser returns the user's non-revoked tokens, newest first.
func (r *Repo) ListActiveByUser(ctx context.Context, userID string) ([]entity.PersonalAccessToken, error) {
	var rows []entity.PersonalAccessToken
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

// FindByHash looks up an active token by its SHA-256 hash. Returns
// gorm.ErrRecordNotFound when no active row matches.
func (r *Repo) FindByHash(ctx context.Context, hash string) (*entity.PersonalAccessToken, error) {
	var t entity.PersonalAccessToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL", hash).
		First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetForUser loads a token belonging to userID. Used by the revoke
// handler to enforce ownership before mutating.
func (r *Repo) GetForUser(ctx context.Context, id, userID string) (*entity.PersonalAccessToken, error) {
	var t entity.PersonalAccessToken
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Revoke stamps RevokedAt on a token belonging to userID. No-op when
// the row is already revoked.
func (r *Repo) Revoke(ctx context.Context, id, userID string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.PersonalAccessToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, userID).
		Update("revoked_at", &now).Error
}

// TouchLastUsed stamps LastUsedAt = now. Cheap fire-and-forget call
// from the MCP auth middleware.
func (r *Repo) TouchLastUsed(ctx context.Context, id string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.PersonalAccessToken{}).
		Where("id = ?", id).
		Update("last_used_at", &now).Error
}

// ListAllActive returns every non-revoked token across all users,
// newest first. Drives the admin MCP page; not exposed to non-admin
// callers.
func (r *Repo) ListAllActive(ctx context.Context) ([]entity.PersonalAccessToken, error) {
	var rows []entity.PersonalAccessToken
	err := r.db.WithContext(ctx).
		Where("revoked_at IS NULL").
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

// RevokeAny stamps RevokedAt on a token regardless of owner. Used by
// the admin override path; the user-facing Revoke still enforces
// ownership.
func (r *Repo) RevokeAny(ctx context.Context, id string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.PersonalAccessToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", &now).Error
}
