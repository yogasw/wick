// Package cliserver keeps long-lived CLI server processes (opencode serve,
// omp --mode rpc) for the providers that hand turns to one instead of
// spawning a process per turn.
//
// The manager is transport-agnostic: it only knows a server as a Handle
// (pid, kill, done). Each provider brings its own start function and talks
// to the server however it wants (HTTP for opencode, stdio JSON for omp).
//
// Rules every server follows, whatever the provider:
//   - lazy: started by the first turn that needs it, never at boot;
//   - idle reaper: killed once no lease touched it for its idle window;
//     the window cannot be zero (it falls back to DefaultIdle);
//   - crash: a server whose process died is forgotten and the next turn
//     starts a fresh one; a failed start is not cached;
//   - stale: when an instance's settings move on (another key, or server
//     mode off) its old servers finish the turns they have and are killed
//     once they have none;
//   - limit: at most Spec.Turns turns run at once on one server, the rest
//     wait for a slot;
//   - shutdown: Shutdown kills every server (wick shutdown / upgrade).
package cliserver

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// DefaultIdle is the idle window when the spec sets none.
	DefaultIdle = 10 * time.Minute
	// ReapInterval is how often the background reaper looks for idle servers.
	ReapInterval = 30 * time.Second
)

// ErrShuttingDown is Acquire's answer once Shutdown ran.
var ErrShuttingDown = errors.New("cli server manager is shutting down")

// Handle is a started server as the manager sees it.
type Handle interface {
	Pid() int
	// Kill stops the process; it must be safe to call more than once.
	Kill()
	// Done is closed once the process has exited.
	Done() <-chan struct{}
}

// Spec is what the manager needs to know about one server.
type Spec struct {
	// Instance groups servers for Retire.
	Instance string
	// Group scopes the stale sweep: acquiring a key retires the group's
	// servers with other keys. Empty = Instance (one server per instance,
	// opencode); omp sets it per session so sessions never retire each
	// other.
	Group string
	// Key identifies the server: two specs with the same key share one.
	Key string
	// Idle is the idle-kill window; <= 0 means DefaultIdle.
	Idle time.Duration
	// Turns caps turns running at once; <= 0 means the manager default.
	Turns int
	// MaxAge, when > 0, retires a server that has been up this long: the
	// next turn gets a fresh one and the old one dies after its last turn.
	MaxAge time.Duration
}

type server[H Handle] struct {
	key      string
	instance string
	group    string
	ready    chan struct{} // closed once h / err are set
	h        H
	started  bool // h is a live handle (false: start failed or pending)
	err      error
	slots    chan struct{}
	born     time.Time

	// guarded by Manager.mu
	active   int // leases held (queued or running)
	lastUsed time.Time
	idle     time.Duration
	stale    bool
}

func (s *server[H]) dead() bool {
	if !s.started {
		return false
	}
	select {
	case <-s.h.Done():
		return true
	default:
		return false
	}
}

func (s *server[H]) isReady() bool {
	select {
	case <-s.ready:
		return true
	default:
		return false
	}
}

// Manager owns every server of one provider in this wick process.
type Manager[H Handle] struct {
	// Name labels log lines ("opencode", "omp").
	Name string
	// Now is the clock; swapped in tests.
	Now func() time.Time
	// Every is the reaper interval; set before the first Acquire.
	Every time.Duration
	// DefaultTurns is the slot count when a spec sets none.
	DefaultTurns int
	// OnStop runs after a server is killed or found dead and forgotten
	// (idle, stale, crash, shutdown). nil = nothing.
	OnStop func(key string, h H)

	mu      sync.Mutex
	servers map[string]*server[H]
	once    sync.Once
	stop    chan struct{}
	closed  bool
}

// New returns a manager whose servers default to turns concurrent turns.
func New[H Handle](name string, turns int) *Manager[H] {
	if turns <= 0 {
		turns = 1
	}
	return &Manager[H]{Name: name, Now: time.Now, Every: ReapInterval, DefaultTurns: turns,
		servers: map[string]*server[H]{}, stop: make(chan struct{})}
}

// Lease is one turn's hold on a server: while held the server is never
// reaped. Release exactly once (extra calls are no-ops).
type Lease[H Handle] struct {
	m    *Manager[H]
	s    *server[H]
	slot bool
	once sync.Once
	// H is the server this lease holds.
	H H
	// Fresh is true for the lease whose Acquire started the server.
	Fresh bool
}

// Acquire returns a lease on the server for spec, starting it with start
// when there is none (or the previous one died or aged out).
func (m *Manager[H]) Acquire(ctx context.Context, spec Spec, start func(ctx context.Context) (H, error)) (*Lease[H], error) {
	m.once.Do(func() { go m.reapLoop() })
	idle := spec.Idle
	if idle <= 0 {
		idle = DefaultIdle
	}
	turns := spec.Turns
	if turns <= 0 {
		turns = m.DefaultTurns
	}
	key := spec.Key
	group := spec.Group
	if group == "" {
		group = spec.Instance
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrShuttingDown
	}
	var victims []*server[H]
	s := m.servers[key]
	if s != nil && s.isReady() {
		switch {
		case s.err != nil || s.dead():
			delete(m.servers, key)
			if s.started && s.active == 0 {
				victims = append(victims, s)
			}
			s = nil
		case spec.MaxAge > 0 && m.Now().Sub(s.born) >= spec.MaxAge:
			// Aged out: a fresh one serves from now on; the old one
			// dies now when idle, else after its last turn (put).
			s.stale = true
			delete(m.servers, key)
			if s.active == 0 {
				victims = append(victims, s)
			}
			s = nil
		}
	}
	fresh := false
	if s == nil {
		s = &server[H]{key: key, instance: spec.Instance, group: group, ready: make(chan struct{}), slots: make(chan struct{}, turns)}
		m.servers[key] = s
		fresh = true
	}
	s.active++
	s.idle = idle
	s.lastUsed = m.Now()
	victims = append(victims, m.sweepLocked(group, key)...)
	m.mu.Unlock()
	m.killAll(victims, "stale server stopped")

	if fresh {
		h, err := start(ctx)
		m.mu.Lock()
		s.err = err
		if err == nil {
			s.h, s.started, s.born = h, true, m.Now()
		} else if m.servers[key] == s {
			delete(m.servers, key)
		}
		m.mu.Unlock()
		close(s.ready)
		if err == nil {
			log.Info().Str("instance", spec.Instance).Str("key", key).Int("pid", h.Pid()).
				Dur("idle", idle).Msg("agents." + m.Name + ": server started")
		}
	}
	select {
	case <-s.ready:
	case <-ctx.Done():
		m.put(s)
		return nil, ctx.Err()
	}
	if s.err != nil {
		m.put(s)
		return nil, s.err
	}
	return &Lease[H]{m: m, s: s, H: s.h, Fresh: fresh}, nil
}

// Release gives the lease back and frees its slot.
func (l *Lease[H]) Release() {
	l.once.Do(func() { l.DropSlot(); l.m.put(l.s) })
}

// WaitSlot blocks until the server has room for one more running turn.
func (l *Lease[H]) WaitSlot(ctx context.Context) error {
	select {
	case l.s.slots <- struct{}{}:
		l.slot = true
		return nil
	case <-l.H.Done():
		return errors.New(l.m.Name + " server exited")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DropSlot frees the running-turn slot early (Release does it too).
func (l *Lease[H]) DropSlot() {
	if l.slot {
		l.slot = false
		<-l.s.slots
	}
}

// Dead reports whether the lease's server process has exited.
func (l *Lease[H]) Dead() bool { return l.s.dead() }

// Discard marks the lease's server as not to be reused (e.g. its protocol
// got out of step): it is killed once its last lease is released.
func (l *Lease[H]) Discard() {
	l.m.mu.Lock()
	l.s.stale = true
	if l.m.servers[l.s.key] == l.s {
		delete(l.m.servers, l.s.key)
	}
	l.m.mu.Unlock()
}

func (m *Manager[H]) put(s *server[H]) {
	m.mu.Lock()
	s.active--
	s.lastUsed = m.Now()
	var victims []*server[H]
	if s.stale && s.active == 0 {
		victims = m.dropLocked(s)
		if len(victims) == 0 && s.started {
			victims = []*server[H]{s} // already out of the map (aged out / discarded)
		}
	}
	m.mu.Unlock()
	m.killAll(victims, "stale server stopped")
}

// Retire marks every server of instance stale (server mode switched off):
// idle ones die now, busy ones when their last turn ends.
func (m *Manager[H]) Retire(instance string) {
	m.mu.Lock()
	victims := m.retireLocked(instance, "")
	m.mu.Unlock()
	m.killAll(victims, "stale server stopped")
}

// RetireKey is Retire for one server.
func (m *Manager[H]) RetireKey(key string) {
	m.mu.Lock()
	var victims []*server[H]
	if s := m.servers[key]; s != nil {
		s.stale = true
		if s.active == 0 {
			victims = m.dropLocked(s)
		} else {
			delete(m.servers, key)
		}
	}
	m.mu.Unlock()
	m.killAll(victims, "stale server stopped")
}

// RetireGroups is Retire for every server whose (instance, group) match
// reports true: a transcript another instance now writes must not stay
// open, stale, in a process that could serve it again.
func (m *Manager[H]) RetireGroups(match func(instance, group string) bool) {
	m.mu.Lock()
	victims := m.markLocked(func(s *server[H]) bool { return match(s.instance, s.group) }, "")
	m.mu.Unlock()
	m.killAll(victims, "stale server stopped")
}

// RetireIdle stops every warm server that is idle — started, alive, not
// stale, and holding no lease (no turn running, none queued for it) —
// unless keep(instance, group) says it serves the spawn about to start.
// Oldest-idle first. Returns how many it stopped. A busy or queued server
// is never touched: it stays until its last lease is released.
func (m *Manager[H]) RetireIdle(keep func(instance, group string) bool) int {
	m.mu.Lock()
	var idle []*server[H]
	for _, s := range m.servers {
		if !s.isReady() || !s.started || s.stale || s.dead() || s.active > 0 {
			continue
		}
		if keep != nil && keep(s.instance, s.group) {
			continue
		}
		idle = append(idle, s)
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].lastUsed.Before(idle[j].lastUsed) })
	var victims []*server[H]
	for _, s := range idle {
		s.stale = true
		victims = append(victims, m.dropLocked(s)...)
	}
	m.mu.Unlock()
	m.killAll(victims, "idle server stopped so another spawn gets its memory")
	return len(victims)
}

func (m *Manager[H]) retireLocked(instance, keep string) []*server[H] {
	return m.markLocked(func(s *server[H]) bool { return s.instance == instance }, keep)
}

func (m *Manager[H]) sweepLocked(group, keep string) []*server[H] {
	return m.markLocked(func(s *server[H]) bool { return s.group == group }, keep)
}

// markLocked marks matching servers other than keep stale and returns the
// idle ones, already removed from the map.
func (m *Manager[H]) markLocked(match func(*server[H]) bool, keep string) []*server[H] {
	var victims []*server[H]
	for k, s := range m.servers {
		if !match(s) || k == keep {
			continue
		}
		s.stale = true
		if s.active == 0 {
			victims = append(victims, m.dropLocked(s)...)
		}
	}
	return victims
}

// dropLocked forgets s; returns it for killing when it is a live server.
func (m *Manager[H]) dropLocked(s *server[H]) []*server[H] {
	if m.servers[s.key] == s {
		delete(m.servers, s.key)
	}
	if s.isReady() && s.started {
		return []*server[H]{s}
	}
	return nil
}

func (m *Manager[H]) killAll(ss []*server[H], why string) {
	for _, s := range ss {
		log.Info().Str("key", s.key).Int("pid", s.h.Pid()).Msg("agents." + m.Name + ": " + why)
		s.h.Kill()
		if m.OnStop != nil {
			m.OnStop(s.key, s.h)
		}
	}
}

// Len is how many servers the manager tracks (tests, status).
func (m *Manager[H]) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers)
}

// Keys lists the tracked server keys (tests, status).
func (m *Manager[H]) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.servers))
	for k := range m.servers {
		keys = append(keys, k)
	}
	return keys
}

// Live returns the handle for key when that server is up and not stale.
func (m *Manager[H]) Live(key string) (H, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var zero H
	s := m.servers[key]
	if s == nil || !s.isReady() || !s.started || s.stale || s.dead() {
		return zero, false
	}
	return s.h, true
}

// LiveInGroup returns a handle of any up, non-stale server of group (the
// Spec.Group it was acquired with, Instance when that was empty). For
// read-only side requests that do not care which key serves them; no
// lease is taken, so the server may still be reaped under the caller.
func (m *Manager[H]) LiveInGroup(group string) (H, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var zero H
	for _, s := range m.servers {
		if s.group == group && s.isReady() && s.started && !s.stale && !s.dead() {
			return s.h, true
		}
	}
	return zero, false
}

// reapLoop runs for the life of the process: it is the only thing that
// ever stops an idle server, so there is no switch to turn it off.
func (m *Manager[H]) reapLoop() {
	t := time.NewTicker(m.Every)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.Reap()
		}
	}
}

// Reap kills every server no lease has touched for its idle window, and
// forgets servers that died on their own. Returns the keys it killed.
func (m *Manager[H]) Reap() []string {
	now := m.Now()
	var victims, gone []*server[H]
	m.mu.Lock()
	for k, s := range m.servers {
		if !s.isReady() {
			continue // still starting
		}
		if s.err != nil || s.dead() {
			if s.active == 0 {
				delete(m.servers, k)
				if s.started {
					gone = append(gone, s)
				}
			}
			continue
		}
		if s.active == 0 && now.Sub(s.lastUsed) >= s.idle {
			delete(m.servers, k)
			victims = append(victims, s)
		}
	}
	m.mu.Unlock()
	var keys []string
	m.killAll(victims, "idle server killed")
	for _, s := range victims {
		keys = append(keys, s.key)
	}
	if m.OnStop != nil {
		for _, s := range gone {
			m.OnStop(s.key, s.h)
		}
	}
	return keys
}

// Shutdown kills every server and refuses new leases.
func (m *Manager[H]) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.stop)
	all := make([]*server[H], 0, len(m.servers))
	for _, s := range m.servers {
		all = append(all, s)
	}
	m.servers = map[string]*server[H]{}
	m.mu.Unlock()
	for _, s := range all {
		<-s.ready
		if s.started {
			s.h.Kill()
			if m.OnStop != nil {
				m.OnStop(s.key, s.h)
			}
		}
	}
}
