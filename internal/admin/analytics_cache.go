package admin

import (
	"os"
	"strings"
	"sync"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// analytics_cache.go keeps the expensive half of the page in memory.
//
// The page's cost is not arithmetic, it is I/O: every render reads
// meta.json (and agents.json) for every session on disk — ~2000 files on
// this install — and it does that again for each range change, each
// channel filter, each reload. The numbers are cheap once the files are
// parsed.
//
// So the parsed facts are cached, keyed by session id, and validated by
// the file's own mtime+size rather than by a TTL. That distinction is the
// point: a TTL is a guess that is either too short to help or long enough
// to serve numbers that are quietly wrong. A stat is ~10µs, so checking
// 2000 of them costs a few milliseconds and cannot be stale — a session
// that changed is re-read, one that did not is free.
//
// Sessions that have been deleted fall out on the next pass: the cache is
// rebuilt from the ids actually seen, so it stays bounded by what exists
// rather than by everything the process has ever read.

// sessionFacts is everything the analytics need from one session. Keeping
// this narrow is what makes the cache small: the transcript, the notes and
// the body never enter it.
type sessionFacts struct {
	mod  time.Time
	size int64

	Channel      string
	InstanceKey  string
	InstanceOwn  string
	ProjectID    string
	Agent        string
	Label        string
	TokenID      string
	TokenName    string
	CreatedAt    time.Time
	LastActive   time.Time
	People       []string
	AutoResolved bool
	// Providers is one entry per distinct provider key the session ran on,
	// with the model it was pinned to ("(default)" when none).
	Providers []providerUse
}

type providerUse struct{ Key, Model string }

type factsCache struct {
	mu sync.Mutex
	m  map[string]sessionFacts
}

var sessionCache = &factsCache{m: map[string]sessionFacts{}}

// get returns the facts for one session, reading from disk only when the
// file has changed since the cached copy.
//
// A stat failure is not cached: the file may be mid-write, and remembering
// an error would keep that session missing from the page until the process
// restarted.
func (c *factsCache) get(layout agentconfig.Layout, id string) (sessionFacts, bool) {
	metaPath := layout.SessionMeta(id)
	st, err := os.Stat(metaPath)
	if err != nil {
		return sessionFacts{}, false
	}

	c.mu.Lock()
	hit, ok := c.m[id]
	c.mu.Unlock()
	if ok && hit.mod.Equal(st.ModTime()) && hit.size == st.Size() {
		return hit, true
	}

	sess, err := session.Load(layout, id)
	if err != nil {
		return sessionFacts{}, false
	}
	f := factsFrom(id, sess)
	f.mod, f.size = st.ModTime(), st.Size()

	c.mu.Lock()
	c.m[id] = f
	c.mu.Unlock()
	return f, true
}

// keep drops every session not in the given set. Called once per render
// with the ids that exist, so a deleted session stops being remembered.
func (c *factsCache) keep(alive map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.m {
		if !alive[id] {
			delete(c.m, id)
		}
	}
}

// size reports how many sessions are cached. Test-facing.
func (c *factsCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// factsFrom reduces a loaded session to what the page needs.
func factsFrom(id string, sess session.Session) sessionFacts {
	m := sess.Meta
	channel := string(m.Origin)
	if channel == "" {
		channel = "ui"
	}
	projectID := m.ProjectID
	if projectID == "" {
		projectID = "(none)"
	}
	key, owner := channelInstanceOf(id, channel, m)

	f := sessionFacts{
		Channel:     channel,
		InstanceKey: key,
		InstanceOwn: owner,
		ProjectID:   projectID,
		Agent:       m.ActiveAgent,
		Label:       m.Label,
		TokenID:     m.TokenID,
		TokenName:   m.TokenName,
		CreatedAt:   m.CreatedAt,
		LastActive:  m.LastActive,
		People:      append([]string(nil), m.People()...),
	}
	seen := map[string]bool{}
	for _, a := range sess.Agents {
		k := strings.TrimSpace(a.Provider)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		model := strings.TrimSpace(a.ModelID)
		if model == "" {
			// Not a gap in the record: no pin means the provider picked its
			// own default, which is a real answer worth showing.
			model = "(default)"
		}
		f.Providers = append(f.Providers, providerUse{Key: k, Model: model})
	}
	return f
}
