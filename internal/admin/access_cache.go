package admin

import (
	"context"
	"sync"
	"time"
)

// The access data, cached in process.
//
// Every admin listing asks the same four questions before it can count
// anything: which approved users exist, who the admins are, which filter tags
// each path carries, and who carries each tag. They are identical on
// /admin/tools, /admin/connectors, /admin/projects and the rest, so paying for
// them again on every tab switch is pure repetition — and on a database 30ms
// away it is the difference between a page that opens and a page that waits.
//
// The whole set is small: users, tags and tool_tags are hundreds of rows, not
// millions, so it is loaded whole in three queries and answered from memory
// afterwards.
//
// Freshness comes from INVALIDATION, not from the clock: every repo method
// that writes a user, a tag or a tag assignment drops the cache, so an edit
// made here is visible on the very next render. The TTL is only a backstop
// for writes this process did not make — another replica, a direct DB edit,
// or the owner tags that internal/tags writes on its own.
const accessCacheTTL = 30 * time.Second

// accessData is the whole cached set.
type accessData struct {
	totalApproved int
	approved      map[string]bool   // approved user ids
	users         []adminUser       // every approved user, name order
	admins        []adminUser       // approved users holding the admin role
	pathTags      map[string][]string // tool_path → its FILTER tag ids
	tagHolders    map[string]map[string]bool // tag id → approved user ids
	tagNames      map[string]string   // tag id → name, for the "via" chips
}

type accessCache struct {
	mu   sync.Mutex
	at   time.Time
	data *accessData
}

// invalidate drops the cache. Called by every write below, so the next read
// reloads rather than serving an edit-old answer.
func (c *accessCache) invalidate() {
	c.mu.Lock()
	c.at = time.Time{}
	c.data = nil
	c.mu.Unlock()
}

// access returns the cached set, loading it when cold or expired.
func (r *repo) access(ctx context.Context) *accessData {
	r.cache.mu.Lock()
	defer r.cache.mu.Unlock()
	if r.cache.data != nil && time.Since(r.cache.at) < accessCacheTTL {
		return r.cache.data
	}
	d := r.loadAccessData(ctx)
	r.cache.at = time.Now()
	r.cache.data = d
	return d
}

// loadAccessData reads the whole set in three queries.
func (r *repo) loadAccessData(ctx context.Context) *accessData {
	d := &accessData{
		approved:   map[string]bool{},
		pathTags:   map[string][]string{},
		tagHolders: map[string]map[string]bool{},
		tagNames:   map[string]string{},
	}

	// 1. Every approved user, with the role — covers total, the id set, and
	//    the admin list in one pass.
	var users []struct {
		ID      string
		Name    string
		Email   string
		Role    string
		IsOwner bool
	}
	r.db.WithContext(ctx).
		Table("users").
		Select("CAST(id AS TEXT) as id, name, email, role, is_owner").
		Where("approved = ?", true).
		Order("name asc, email asc").
		Scan(&users)
	for _, u := range users {
		d.approved[u.ID] = true
		d.users = append(d.users, adminUser{
			ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, Approved: true,
		})
		if u.Role == "admin" || u.IsOwner {
			d.admins = append(d.admins, adminUser{
				ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, Approved: true,
			})
		}
	}
	d.totalApproved = len(users)

	// 2. Which FILTER tags gate each path.
	var links []struct {
		ToolPath string
		TagID    string
		Name     string
	}
	r.db.WithContext(ctx).
		Table("tool_tags tt").
		Select("tt.tool_path as tool_path, tt.tag_id as tag_id, t.name as name").
		Joins("JOIN tags t ON t.id = tt.tag_id").
		Where("t.is_filter = ?", true).
		Scan(&links)
	for _, l := range links {
		d.pathTags[l.ToolPath] = append(d.pathTags[l.ToolPath], l.TagID)
		d.tagNames[l.TagID] = l.Name
	}

	// 3. Who carries each tag (approved users only — a pending account is
	//    not access).
	var holders []struct {
		TagID  string
		UserID string
		Name   string
	}
	r.db.WithContext(ctx).
		Table("user_tags ut").
		Select("ut.tag_id as tag_id, CAST(ut.user_id AS TEXT) as user_id, t.name as name").
		Joins("JOIN users u ON CAST(u.id AS TEXT) = CAST(ut.user_id AS TEXT)").
		Joins("JOIN tags t ON t.id = ut.tag_id").
		Where("u.approved = ?", true).
		Scan(&holders)
	for _, h := range holders {
		if d.tagHolders[h.TagID] == nil {
			d.tagHolders[h.TagID] = map[string]bool{}
		}
		d.tagHolders[h.TagID][h.UserID] = true
		d.tagNames[h.TagID] = h.Name
	}
	return d
}

// usersByID resolves ids to the projection the modals render, from the cached
// approved set. Unknown ids are skipped — an unapproved or deleted user is
// not reach, and silently dropping them is the same answer the queries gave.
func (d *accessData) usersByID(all []adminUser, ids []string) []adminUser {
	byID := make(map[string]adminUser, len(all))
	for _, u := range all {
		byID[u.ID] = u
	}
	out := make([]adminUser, 0, len(ids))
	for _, id := range ids {
		if u, ok := byID[id]; ok {
			out = append(out, u)
		}
	}
	return out
}
