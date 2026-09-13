package admin

import (
	"context"
	"sync"
	"time"
)

// The user-side sets, cached across tabs.
//
// Every admin listing needs the same three answers before it can count
// anything: how many approved users exist, which ids those are, and who the
// admins are. They are identical on /admin/tools, /admin/connectors,
// /admin/projects and the rest, so paying for them again on every tab switch
// is three round trips of pure repetition.
//
// What is NOT cached, and why: the tag-holder sets. Those are what the tag
// pickers on these very pages write, and a count that lags the edit which
// produced it is worse than a slow one. The split is the point — the cached
// half changes on /admin/users, the live half changes on the page you are
// looking at.
//
// The TTL is a backstop, not the mechanism: every handler that changes a
// user's approval, role or tags drops the cache outright, so the window
// where it can be stale is a mutation this process did not make (another
// replica, a direct DB edit) within userSetTTL.
const userSetTTL = 30 * time.Second

type userSets struct {
	total    int
	approved map[string]bool
	admins   []adminUser
}

type userSetCache struct {
	mu   sync.Mutex
	at   time.Time
	sets userSets
}

// userSets returns the three sets, loading them at most once per TTL.
func (h *Handler) userSets(ctx context.Context) userSets {
	h.userCache.mu.Lock()
	defer h.userCache.mu.Unlock()
	if !h.userCache.at.IsZero() && time.Since(h.userCache.at) < userSetTTL {
		return h.userCache.sets
	}
	s := userSets{total: h.repo.ApprovedUserCount(ctx)}
	if approved, err := h.repo.ApprovedUserIDs(ctx); err == nil {
		s.approved = approved
	}
	if admins, err := h.repo.AdminUsers(ctx); err == nil {
		s.admins = admins
	}
	h.userCache.at = time.Now()
	h.userCache.sets = s
	return s
}

// invalidateUserSets drops the cache. Called by every handler that changes
// who exists, who is approved, or who is an admin — so the next page render
// sees the change immediately rather than up to a TTL later.
func (h *Handler) invalidateUserSets() {
	h.userCache.mu.Lock()
	h.userCache.at = time.Time{}
	h.userCache.mu.Unlock()
}
