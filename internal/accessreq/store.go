package accessreq

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Store is the gorm-backed persistence of requests, grants, rules and
// facts.
type Store struct{ db *gorm.DB }

// NewStore wraps db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) createRequest(ctx context.Context, r *entity.AccessRequest, items []entity.AccessRequestItem) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(r).Error; err != nil {
			return err
		}
		for i := range items {
			if err := tx.Create(&items[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) request(ctx context.Context, id string) (*entity.AccessRequest, []entity.AccessRequestItem, error) {
	var r entity.AccessRequest
	if err := s.db.WithContext(ctx).First(&r, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	var items []entity.AccessRequestItem
	if err := s.db.WithContext(ctx).Where("request_id = ?", id).Order("idx").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	return &r, items, nil
}

func (s *Store) setOrigin(ctx context.Context, id, origin string) error {
	return s.db.WithContext(ctx).Model(&entity.AccessRequest{}).Where("id = ?", id).
		Updates(map[string]any{"origin": origin, "updated_at": time.Now()}).Error
}

func (s *Store) setRequestStatus(ctx context.Context, id, from, to string) (bool, error) {
	res := s.db.WithContext(ctx).Model(&entity.AccessRequest{}).Where("id = ? AND status = ?", id, from).
		Updates(map[string]any{"status": to, "updated_at": time.Now()})
	return res.RowsAffected == 1, res.Error
}

// claimItem moves an item out of pending atomically: two people clicking
// at once, or one clicking twice, see exactly one winner.
func (s *Store) claimItem(ctx context.Context, itemID, status, mode, by, note string, at time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).
		Where("id = ? AND status = ?", itemID, ItemPending).
		Updates(map[string]any{"status": status, "mode": mode, "decided_by": by, "decided_at": at, "note": note})
	return res.RowsAffected == 1, res.Error
}

func (s *Store) finishItem(ctx context.Context, itemID, status, runID, result, errText string) error {
	return s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).Where("id = ?", itemID).
		Updates(map[string]any{"status": status, "run_id": runID, "result": result, "error": errText}).Error
}

func (s *Store) pendingCount(ctx context.Context, requestID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).
		Where("request_id = ? AND status IN ?", requestID, []string{ItemPending, ItemRunning}).Count(&n).Error
	return n, err
}

func (s *Store) countOpen(ctx context.Context, sessionID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.AccessRequest{}).
		Where("session_id = ? AND status = ?", sessionID, StatusPending).Count(&n).Error
	return n, err
}

func (s *Store) listRequests(ctx context.Context, status string, limit int) ([]entity.AccessRequest, error) {
	q := s.db.WithContext(ctx).Order("created_at DESC").Limit(limit)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []entity.AccessRequest
	return out, q.Find(&out).Error
}

func (s *Store) addGrant(ctx context.Context, g *entity.AccessGrant) error {
	return s.db.WithContext(ctx).Create(g).Error
}

func (s *Store) grants(ctx context.Context, connectorID, opKey string, scopes map[string]string) ([]entity.AccessGrant, error) {
	var out []entity.AccessGrant
	q := s.db.WithContext(ctx).Where("connector_id = ? AND op_key = ? AND revoked_at IS NULL", connectorID, opKey)
	var or *gorm.DB
	for typ, id := range scopes {
		if id == "" {
			continue
		}
		c := s.db.Where("scope_type = ? AND scope_id = ?", typ, id)
		if or == nil {
			or = c
		} else {
			or = or.Or(c)
		}
	}
	if or == nil {
		return nil, nil
	}
	return out, q.Where(or).Order("created_at").Find(&out).Error
}

func (s *Store) listGrants(ctx context.Context, scopeType, scopeID string) ([]entity.AccessGrant, error) {
	var out []entity.AccessGrant
	q := s.db.WithContext(ctx).Where("revoked_at IS NULL").Order("created_at DESC")
	if scopeType != "" {
		q = q.Where("scope_type = ? AND scope_id = ?", scopeType, scopeID)
	}
	return out, q.Find(&out).Error
}

func (s *Store) grant(ctx context.Context, id string) (*entity.AccessGrant, error) {
	var g entity.AccessGrant
	if err := s.db.WithContext(ctx).First(&g, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &g, nil
}

func (s *Store) revokeGrant(ctx context.Context, id, by string) error {
	now := time.Now()
	return s.db.WithContext(ctx).Model(&entity.AccessGrant{}).Where("id = ? AND revoked_at IS NULL", id).
		Updates(map[string]any{"revoked_at": now, "revoked_by": by}).Error
}

func (s *Store) rules(ctx context.Context) ([]entity.AccessRule, error) {
	var out []entity.AccessRule
	return out, s.db.WithContext(ctx).Order("created_at").Find(&out).Error
}

func (s *Store) rule(ctx context.Context, id string) (*entity.AccessRule, error) {
	var r entity.AccessRule
	if err := s.db.WithContext(ctx).First(&r, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) saveRule(ctx context.Context, r *entity.AccessRule, rev *entity.AccessRuleRevision, create bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if create {
			if err := tx.Create(r).Error; err != nil {
				return err
			}
		} else if err := tx.Save(r).Error; err != nil {
			return err
		}
		return tx.Create(rev).Error
	})
}

func (s *Store) deleteRule(ctx context.Context, r *entity.AccessRule, rev *entity.AccessRuleRevision) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&entity.AccessRule{}, "id = ?", r.ID).Error; err != nil {
			return err
		}
		return tx.Create(rev).Error
	})
}

func (s *Store) ruleHistory(ctx context.Context, ruleID string, limit int) ([]entity.AccessRuleRevision, error) {
	var out []entity.AccessRuleRevision
	return out, s.db.WithContext(ctx).Where("rule_id = ?", ruleID).Order("rev DESC").Limit(limit).Find(&out).Error
}

func (s *Store) fact(ctx context.Context, fact, subject string) (string, bool) {
	var f entity.AccessFact
	if err := s.db.WithContext(ctx).First(&f, "fact = ? AND subject = ?", fact, subject).Error; err != nil {
		return "", false
	}
	return f.Value, true
}

func (s *Store) facts(ctx context.Context) ([]entity.AccessFact, error) {
	var out []entity.AccessFact
	return out, s.db.WithContext(ctx).Order("fact, subject").Find(&out).Error
}

func (s *Store) setFact(ctx context.Context, f entity.AccessFact) error {
	db := s.db.WithContext(ctx)
	res := db.Model(&entity.AccessFact{}).Where("fact = ? AND subject = ?", f.Fact, f.Subject).
		Updates(map[string]any{"value": f.Value, "updated_by": f.UpdatedBy, "updated_at": f.UpdatedAt})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return db.Create(&f).Error
	}
	return nil
}

func (s *Store) deleteFact(ctx context.Context, fact, subject string) error {
	return s.db.WithContext(ctx).Delete(&entity.AccessFact{}, "fact = ? AND subject = ?", fact, subject).Error
}
