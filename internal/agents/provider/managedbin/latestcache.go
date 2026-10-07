package managedbin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
)

// latestcache.go is the per-type "newest release + release list" cache,
// the same shape as wick's own version cache (updater.VersionCache): a
// background loop refreshes it on boot (jittered) and every LatestTTL,
// concurrent refreshes coalesce, and request handlers only READ the
// snapshot — opening the Providers page never talks to GitHub. The
// snapshot is persisted next to the managed dirs so a reload or restart
// starts warm instead of hitting GitHub again.

// LatestSnapshot is what the last GitHub check learned for one type.
// Assets are stripped: install jobs re-resolve the release themselves.
type LatestSnapshot struct {
	Latest    *Release  `json:"latest,omitempty"`
	Releases  []Release `json:"releases,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Err       string    `json:"err,omitempty"`
}

const (
	// LatestTTL is how old a snapshot may get before the loop refreshes it.
	LatestTTL = time.Hour
	// ForceRefreshMinGap bounds the "Check for update" button.
	ForceRefreshMinGap = 60 * time.Second
	// latestErrRetry is the sooner retry after a failed check.
	latestErrRetry = 10 * time.Minute
	// latestBootJitter spreads the first check after boot.
	latestBootJitter = 30 * time.Second
	releaseListSize  = 20
	latestCacheFile  = "latest-cache.json"
)

// ErrRefreshTooSoon is returned by ForceRefreshLatest inside the min gap.
var ErrRefreshTooSoon = errors.New("checked less than a minute ago")

func (m *Manager) latestCachePath() string {
	if r := m.root(); r != "" {
		return filepath.Join(r, latestCacheFile)
	}
	return ""
}

// ensureCacheLocked loads the persisted snapshots once (m.mu held). A
// local file read, never the network.
func (m *Manager) ensureCacheLocked() {
	if m.cacheLoaded {
		return
	}
	p := m.latestCachePath()
	if p == "" {
		return
	}
	m.cacheLoaded = true
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var snaps map[string]LatestSnapshot
	if json.Unmarshal(b, &snaps) != nil {
		return
	}
	for k, v := range snaps {
		if _, ok := m.snaps[k]; !ok {
			m.snaps[k] = v
		}
	}
}

// LatestSnapshot returns the cached answer for typ. No network.
func (m *Manager) LatestSnapshot(typ string) (LatestSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCacheLocked()
	s, ok := m.snaps[typ]
	return s, ok
}

func snapErr(s LatestSnapshot) error {
	if s.Err != "" {
		return errors.New(s.Err)
	}
	return nil
}

// RefreshLatest asks GitHub for typ's newest release and recent releases
// and publishes a new snapshot. A caller arriving while a refresh for
// typ is in flight waits for that one instead of starting another.
func (m *Manager) RefreshLatest(ctx context.Context, typ string) (LatestSnapshot, error) {
	src, ok := Lookup(typ)
	if !ok {
		return LatestSnapshot{}, fmt.Errorf("%s is not a managed binary type", typ)
	}
	m.mu.Lock()
	if ch, busy := m.inflight[typ]; busy {
		m.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return LatestSnapshot{}, ctx.Err()
		}
		s, _ := m.LatestSnapshot(typ)
		return s, snapErr(s)
	}
	ch := make(chan struct{})
	m.inflight[typ] = ch
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.inflight, typ)
		m.mu.Unlock()
		close(ch)
	}()

	s := LatestSnapshot{FetchedAt: time.Now().UTC()}
	if rel, err := m.client.latest(ctx, src.Repo()); err != nil {
		s.Err = err.Error()
	} else {
		rel.Assets = nil
		s.Latest = &rel
	}
	if rs, err := m.client.list(ctx, src.Repo(), releaseListSize); err != nil {
		if s.Err == "" {
			s.Err = err.Error()
		}
	} else {
		for i := range rs {
			rs[i].Assets = nil
		}
		s.Releases = rs
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.mu.Lock()
	m.ensureCacheLocked()
	// A failed check keeps the last good answer visible (with the error).
	if prev, ok := m.snaps[typ]; ok {
		if s.Latest == nil {
			s.Latest = prev.Latest
		}
		if s.Releases == nil {
			s.Releases = prev.Releases
		}
	}
	m.snaps[typ] = s
	all := make(map[string]LatestSnapshot, len(m.snaps))
	for k, v := range m.snaps {
		all[k] = v
	}
	m.mu.Unlock()
	if p := m.latestCachePath(); p != "" {
		if b, err := json.MarshalIndent(all, "", "  "); err == nil {
			if err := writeAtomic(p, b); err != nil {
				log.Warn().Err(err).Msg("managedbin: save latest cache failed")
			}
		}
	}
	return s, snapErr(s)
}

// ForceRefreshLatest is the "Check for update" button: a RefreshLatest
// at most once per ForceRefreshMinGap per type. Inside the gap it returns
// ErrRefreshTooSoon and how long to wait.
func (m *Manager) ForceRefreshLatest(ctx context.Context, typ string) (LatestSnapshot, time.Duration, error) {
	m.mu.Lock()
	if at, ok := m.forcedAt[typ]; ok {
		if wait := ForceRefreshMinGap - time.Since(at); wait > 0 {
			m.mu.Unlock()
			s, _ := m.LatestSnapshot(typ)
			return s, wait, ErrRefreshTooSoon
		}
	}
	m.forcedAt[typ] = time.Now()
	m.mu.Unlock()
	s, err := m.RefreshLatest(ctx, typ)
	return s, 0, err
}

// RunLatest keeps the snapshots fresh until ctx ends: after a jittered
// boot delay it refreshes every enabled type whose snapshot is missing or
// older than LatestTTL (latestErrRetry after a failure), then re-checks
// each minute. A warm file from before a restart is honoured, so a
// reload does not re-hit GitHub.
func (m *Manager) RunLatest(ctx context.Context) {
	boot := time.NewTimer(rand.N(latestBootJitter))
	select {
	case <-ctx.Done():
		boot.Stop()
		return
	case <-boot.C:
	}
	m.refreshStale(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refreshStale(ctx)
		}
	}
}

func (m *Manager) refreshStale(ctx context.Context) {
	for _, typ := range Types() {
		if m.Enabled != nil && !m.Enabled(typ) {
			continue
		}
		s, ok := m.LatestSnapshot(typ)
		maxAge := LatestTTL
		if s.Err != "" {
			maxAge = latestErrRetry
		}
		if ok && time.Since(s.FetchedAt) < maxAge {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if _, err := m.RefreshLatest(cctx, typ); err != nil {
			log.Debug().Err(err).Str("type", typ).Msg("managedbin: latest release check failed")
		}
		cancel()
	}
}
