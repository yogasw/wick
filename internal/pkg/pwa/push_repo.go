package pwa

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type pushRepo struct {
	db *gorm.DB
}

func newPushRepo(db *gorm.DB) *pushRepo {
	return &pushRepo{db: db}
}

func (r *pushRepo) Upsert(ctx context.Context, sub *entity.PushSubscription) error {
	now := time.Now()
	sub.LastSeenAt = &now
	sub.DisabledAt = nil
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "endpoint"}},
		DoUpdates: clause.Assignments(map[string]any{
			"user_id":      sub.UserID,
			"p256dh":       sub.P256dh,
			"auth":         sub.Auth,
			"user_agent":   sub.UserAgent,
			"device_label": sub.DeviceLabel,
			"last_seen_at": now,
			"disabled_at":  nil,
			"updated_at":   now,
		}),
	}).Create(sub).Error
}

func (r *pushRepo) ListActiveByUser(ctx context.Context, userID string) ([]entity.PushSubscription, error) {
	var rows []entity.PushSubscription
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND disabled_at IS NULL", userID).
		Order("last_seen_at DESC, updated_at DESC").
		Find(&rows).Error
	return rows, err
}

// ListAllActive returns every active subscription across every user.
// Used by broadcast paths (e.g. agent lifecycle notifications) that
// fan out to anyone who has notifications enabled, without caring who
// owns the row. Order is stable for deterministic iteration in tests.
func (r *pushRepo) ListAllActive(ctx context.Context) ([]entity.PushSubscription, error) {
	var rows []entity.PushSubscription
	err := r.db.WithContext(ctx).
		Where("disabled_at IS NULL").
		Order("user_id, last_seen_at DESC, updated_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *pushRepo) DisableByEndpoint(ctx context.Context, userID, endpoint string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.PushSubscription{}).
		Where("user_id = ? AND endpoint = ?", userID, endpoint).
		Updates(map[string]any{"disabled_at": now, "updated_at": now}).Error
}

func (r *pushRepo) DisableEndpoint(ctx context.Context, endpoint string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.PushSubscription{}).
		Where("endpoint = ?", endpoint).
		Updates(map[string]any{"disabled_at": now, "updated_at": now}).Error
}
