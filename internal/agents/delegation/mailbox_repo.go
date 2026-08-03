package delegation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Storage for agent-to-agent messages.
//
// The mailbox is a queue, not a bus: messages are addressed, ordered, and
// durable, and nothing is delivered by fan-out. That keeps the cost model
// honest — every delivery is one recipient waking for one batch.

// EnqueueMessage stores one message as queued.
func (r *Repo) EnqueueMessage(ctx context.Context, m *entity.AgentMessage) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	if m.Status == "" {
		m.Status = entity.MessageQueued
	}
	return r.db.WithContext(ctx).Create(m).Error
}

// DrainInbox takes up to max queued messages for one handle, oldest
// first, and marks them delivered in the same transaction.
//
// Claim-then-return rather than read-then-mark: two concurrent drains of
// the same inbox would otherwise hand the same message to the recipient
// twice, and a duplicated instruction is indistinguishable from a repeated
// one to the model reading it.
func (r *Repo) DrainInbox(ctx context.Context, rootID, handle string, max int) ([]entity.AgentMessage, error) {
	if max <= 0 {
		max = inboxBatchMax
	}
	var out []entity.AgentMessage
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("root_id = ? AND to_handle = ? AND status = ?",
			rootID, handle, entity.MessageQueued).
			Order("created_at asc").Limit(max).Find(&out).Error; err != nil {
			return err
		}
		if len(out) == 0 {
			return nil
		}
		ids := make([]string, 0, len(out))
		for i := range out {
			ids = append(ids, out[i].ID)
		}
		now := time.Now()
		return tx.Model(&entity.AgentMessage{}).Where("id IN ?", ids).
			Updates(map[string]any{"status": entity.MessageDelivered, "delivered_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CountQueued reports how far behind an inbox is, for the backpressure cap.
func (r *Repo) CountQueued(ctx context.Context, rootID, handle string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&entity.AgentMessage{}).
		Where("root_id = ? AND to_handle = ? AND status = ?",
			rootID, handle, entity.MessageQueued).Count(&n).Error
	return n, err
}

// MarkAnswered closes an ask once its reply exists.
func (r *Repo) MarkAnswered(ctx context.Context, askID, replyID string) error {
	return r.db.WithContext(ctx).Model(&entity.AgentMessage{}).
		Where("id = ?", askID).
		Updates(map[string]any{"status": entity.MessageAnswered, "reply_to": replyID}).Error
}

// FindReply returns the reply to an ask, or nil when none exists yet.
func (r *Repo) FindReply(ctx context.Context, askID string) (*entity.AgentMessage, error) {
	var m entity.AgentMessage
	err := r.db.WithContext(ctx).
		Where("reply_to = ? AND kind = ?", askID, entity.MessageReply).
		Order("created_at asc").First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// GetMessage loads one message by id, or nil when absent.
func (r *Repo) GetMessage(ctx context.Context, id string) (*entity.AgentMessage, error) {
	var m entity.AgentMessage
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// OldestUnansweredAsk returns the ask a recipient still owes an answer
// to, for the turn-end auto-reply fallback.
func (r *Repo) OldestUnansweredAsk(ctx context.Context, rootID, handle string) (*entity.AgentMessage, error) {
	var m entity.AgentMessage
	err := r.db.WithContext(ctx).
		Where("root_id = ? AND to_handle = ? AND kind = ? AND status = ?",
			rootID, handle, entity.MessageAsk, entity.MessageDelivered).
		Order("created_at asc").First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// ListThread returns every message in a tree, oldest first, for the UI.
func (r *Repo) ListThread(ctx context.Context, rootID string, limit int) ([]entity.AgentMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	var out []entity.AgentMessage
	err := r.db.WithContext(ctx).Where("root_id = ?", rootID).
		Order("created_at asc").Limit(limit).Find(&out).Error
	return out, err
}

// TakenHandles lists the handles already allocated in a tree.
func (r *Repo) TakenHandles(ctx context.Context, rootID string) ([]string, error) {
	var out []string
	err := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ? AND handle <> ''", rootID).
		Pluck("handle", &out).Error
	return out, err
}

// FindByHandle resolves an address inside one tree. Returns nil when the
// handle does not exist there: addressing never crosses trees, so a handle
// borrowed from another conversation simply does not resolve.
func (r *Repo) FindByHandle(ctx context.Context, rootID, handle string) (*entity.AgentDelegation, error) {
	var d entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("root_id = ? AND handle = ?", rootID, handle).First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// BumpHop increments the tree's hop counter and returns the value BEFORE
// the increment, which is the hop this message occupies.
func (r *Repo) BumpHop(ctx context.Context, rootID string) (int, error) {
	var hop int
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var root entity.AgentDelegation
		if err := tx.Where("id = ?", rootID).First(&root).Error; err != nil {
			return err
		}
		hop = root.HopCount
		return tx.Model(&entity.AgentDelegation{}).Where("id = ?", rootID).
			Update("hop_count", hop+1).Error
	})
	return hop, err
}

// ResetHops clears the counter.
//
// Called when a HUMAN posts a turn — the only actor allowed to reset it.
// An agent that could clear its own limit would reach for it exactly when
// it is deepest in a loop and most certain the loop is productive.
func (r *Repo) ResetHops(ctx context.Context, rootID string) error {
	return r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ?", rootID).Update("hop_count", 0).Error
}
