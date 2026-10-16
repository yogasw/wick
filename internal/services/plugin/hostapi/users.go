package hostapi

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// UsersCacheTTL is how long the users list is served from memory.
const UsersCacheTTL = 60 * time.Second

// userCache holds the users list as plugins see it: no password, no role
// beyond the admin flag, no preferences. The directory and the tag lookup
// run outside the lock, so a slow refresh never blocks readers of the
// previous snapshot for longer than a map swap.
type userCache struct {
	dir  UserDirectory
	tags func(ctx context.Context, userIDs []string) (map[string][]string, error)
	ttl  time.Duration
	now  func() time.Time

	mu   sync.Mutex
	snap *userSnapshot
}

type userSnapshot struct {
	at      time.Time
	users   []*hostpb.User
	byID    map[string]*hostpb.User
	byEmail map[string]*hostpb.User
}

func (c *userCache) current() *userSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && c.now().Sub(c.snap.at) < c.ttl {
		return c.snap
	}
	return nil
}

func (c *userCache) load(ctx context.Context) (*userSnapshot, error) {
	if s := c.current(); s != nil {
		return s, nil
	}
	return c.refresh(ctx)
}

// refresh rebuilds the snapshot: one directory query and one bulk tag
// query, whatever the number of users.
func (c *userCache) refresh(ctx context.Context) (*userSnapshot, error) {
	rows, err := c.dir.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	var tags map[string][]string
	if c.tags != nil {
		ids := make([]string, len(rows))
		for i, u := range rows {
			ids[i] = u.ID
		}
		if tags, err = c.tags(ctx, ids); err != nil {
			log.Ctx(ctx).Warn().Err(err).Msg("host api: user tags unavailable, serving users without tags")
		}
	}
	s := &userSnapshot{at: c.now(), users: make([]*hostpb.User, 0, len(rows)),
		byID: make(map[string]*hostpb.User, len(rows)), byEmail: make(map[string]*hostpb.User, len(rows))}
	for _, u := range rows {
		pu := toPBUser(u)
		pu.Tags = tags[u.ID]
		s.users = append(s.users, pu)
		s.byID[u.ID] = pu
		s.byEmail[strings.ToLower(u.Email)] = pu
	}
	c.mu.Lock()
	c.snap = s
	c.mu.Unlock()
	return s, nil
}

func (c *userCache) get(ctx context.Context, id, email string) (*hostpb.User, error) {
	s, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	if id != "" {
		return s.byID[id], nil
	}
	return s.byEmail[strings.ToLower(strings.TrimSpace(email))], nil
}

// identity returns the user a call would run as, refreshing once on a miss
// so a person created inside the TTL is not refused.
func (c *userCache) identity(ctx context.Context, id string) (*hostpb.User, error) {
	u, err := c.get(ctx, id, "")
	if err != nil || u != nil {
		return u, err
	}
	s, err := c.refresh(ctx)
	if err != nil {
		return nil, err
	}
	return s.byID[id], nil
}

func toPBUser(u *entity.User) *hostpb.User {
	return &hostpb.User{Id: u.ID, Name: u.Name, Email: u.Email, Approved: u.Approved,
		Admin: u.IsAdmin(), CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339)}
}
