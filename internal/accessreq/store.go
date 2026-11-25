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

func (s *Store) setItemNote(ctx context.Context, itemID, note string) error {
	return s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).Where("id = ?", itemID).Update("note", note).Error
}

func (s *Store) finishItem(ctx context.Context, itemID, status, runID, result, errText string) error {
	return s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).Where("id = ?", itemID).
		Updates(map[string]any{"status": status, "run_id": runID, "result": result, "error": errText}).Error
}

func (s *Store) pendingCount(ctx context.Context, requestID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).
		Where("request_id = ? AND status IN ?", requestID, []string{ItemPending, ItemRunning, ItemQueued}).Count(&n).Error
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

func (s *Store) grants(ctx context.Context, connectorID, opKey string, scopes map[string]string, now time.Time) ([]entity.AccessGrant, error) {
	var out []entity.AccessGrant
	q := s.db.WithContext(ctx).Where("connector_id = ? AND op_key = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", connectorID, opKey, now)
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

func (s *Store) listGrants(ctx context.Context, scopeType, scopeID string, now time.Time) ([]entity.AccessGrant, error) {
	var out []entity.AccessGrant
	q := s.db.WithContext(ctx).Where("revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", now).Order("created_at DESC")
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

// pruneFinished deletes requests that stopped changing before cutoff and
// are no longer pending, with their items. Their history stays in the
// session's files.
// revokeConnectorGrants ends every live grant on connectorID in a
// session (its session connector was removed).
func (s *Store) revokeConnectorGrants(ctx context.Context, sessionID, connectorID, by string) error {
	return s.db.WithContext(ctx).Model(&entity.AccessGrant{}).
		Where("connector_id = ? AND scope_type = ? AND scope_id = ? AND revoked_at IS NULL", connectorID, "session", sessionID).
		Updates(map[string]any{"revoked_at": time.Now(), "revoked_by": by}).Error
}

// pendingItemsFor lists the pending items on connectorID in a session's
// open requests.
func (s *Store) pendingItemsFor(ctx context.Context, sessionID, connectorID string) ([]entity.AccessRequestItem, error) {
	var out []entity.AccessRequestItem
	open := s.db.Model(&entity.AccessRequest{}).Select("id").Where("session_id = ? AND status = ?", sessionID, "pending")
	return out, s.db.WithContext(ctx).Where("request_id IN (?) AND connector_id = ? AND status = ?", open, connectorID, "pending").Find(&out).Error
}

// openItemsOn counts the items on connectorID still waiting for a person
// or running, in a session's open requests.
func (s *Store) openItemsOn(ctx context.Context, sessionID, connectorID string) (int64, error) {
	var n int64
	open := s.db.Model(&entity.AccessRequest{}).Select("id").Where("session_id = ? AND status = ?", sessionID, StatusPending)
	err := s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).
		Where("request_id IN (?) AND connector_id = ? AND status IN ?", open, connectorID, []string{ItemPending, ItemRunning}).
		Count(&n).Error
	return n, err
}

func (s *Store) pruneFinished(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		old := tx.Model(&entity.AccessRequest{}).Select("id").Where("status <> ? AND updated_at < ?", "pending", cutoff)
		if err := tx.Where("request_id IN (?)", old).Delete(&entity.AccessRequestItem{}).Error; err != nil {
			return err
		}
		res := tx.Where("status <> ? AND updated_at < ?", "pending", cutoff).Delete(&entity.AccessRequest{})
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}

func (s *Store) projectGrants(ctx context.Context, projectID string, now time.Time) ([]entity.AccessGrant, error) {
	var out []entity.AccessGrant
	return out, s.db.WithContext(ctx).Where("project_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", projectID, now).Order("created_at DESC").Find(&out).Error
}

// sessionRequests lists a session's requests, newest first.
func (s *Store) sessionRequests(ctx context.Context, sessionID string, limit int) ([]entity.AccessRequest, error) {
	var out []entity.AccessRequest
	q := s.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at DESC, version DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return out, q.Find(&out).Error
}

// latestByKey is the newest request of a session filed under key.
func (s *Store) latestByKey(ctx context.Context, sessionID, key string) (*entity.AccessRequest, error) {
	var r entity.AccessRequest
	err := s.db.WithContext(ctx).Where("session_id = ? AND req_key = ?", sessionID, key).Order("version DESC, created_at DESC").First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// supersede marks old as replaced by newID (only while it is still
// pending) and points the requests that ran after it at newID.
func (s *Store) supersede(ctx context.Context, oldID, newID string) (bool, error) {
	ok := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&entity.AccessRequest{}).Where("id = ? AND status = ?", oldID, StatusPending).
			Updates(map[string]any{"status": StatusSuperseded, "superseded_by": newID, "updated_at": time.Now()})
		if res.Error != nil {
			return res.Error
		}
		ok = res.RowsAffected == 1
		if !ok {
			return nil
		}
		return tx.Model(&entity.AccessRequest{}).Where("after_id = ? AND id <> ?", oldID, newID).
			Update("after_id", newID).Error
	})
	return ok, err
}

// followers are the requests that run after id.
func (s *Store) followers(ctx context.Context, id string) ([]entity.AccessRequest, error) {
	var out []entity.AccessRequest
	return out, s.db.WithContext(ctx).Where("after_id = ? AND status = ?", id, StatusPending).Order("created_at").Find(&out).Error
}

// moveItems moves a request's items in one of from to status to, with an
// error text (for skipped items).
func (s *Store) moveItems(ctx context.Context, requestID string, from []string, to, errText string) error {
	up := map[string]any{"status": to}
	if errText != "" {
		up["error"] = errText
	}
	return s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).
		Where("request_id = ? AND status IN ?", requestID, from).Updates(up).Error
}

// requestByPrefix finds a session's request by full id or an id prefix
// of at least 8 characters.
func (s *Store) requestByPrefix(ctx context.Context, sessionID, id string) (*entity.AccessRequest, error) {
	var out []entity.AccessRequest
	if err := s.db.WithContext(ctx).Where("session_id = ? AND id LIKE ?", sessionID, id+"%").Limit(2).Find(&out).Error; err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// pendingInThread lists the pending requests whose card sits in a Slack
// thread.
func (s *Store) pendingInThread(ctx context.Context, chatID, threadTS string) ([]entity.AccessRequest, error) {
	var out []entity.AccessRequest
	return out, s.db.WithContext(ctx).Where("status = ? AND origin LIKE ? AND origin LIKE ?", StatusPending,
		`%"slack_channel":"`+chatID+`"%`, `%"slack_thread":"`+threadTS+`"%`).Find(&out).Error
}

func (s *Store) setAfter(ctx context.Context, id, after string) error {
	return s.db.WithContext(ctx).Model(&entity.AccessRequest{}).Where("id = ?", id).Update("after_id", after).Error
}

// queueItems parks approved (running) items until the request they run
// after finished.
func (s *Store) queueItems(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&entity.AccessRequestItem{}).
		Where("id IN ? AND status = ?", ids, ItemRunning).Update("status", ItemQueued).Error
}
