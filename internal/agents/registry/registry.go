// Package registry holds the in-memory cache of on-disk Agents state
// (Registry) plus a mutator wrapper (Manager) and the boot entrypoint
// (Bootstrap). Files in this package:
//
//   - registry.go  — Registry: read-only cache, Reload(), accessors
//   - manager.go   — Manager: disk mutate + cache refresh
//   - bootstrap.go — Bootstrap: ensure layout + default preset, load cache
//
// Files remain the source of truth — the registry is a cache populated
// at boot via Reload() and refreshed on demand by Manager mutators.
// Filesystem watching is intentionally out of scope; mutation paths
// refresh the relevant entry inline.
package registry

import (
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
)

// Registry is the in-memory view of the on-disk agents state.
//
// Concurrency: protected by RWMutex. Read-heavy access patterns (UI
// listings, dashboard) hold RLock; mutators hold Lock.
type Registry struct {
	layout config.Layout

	mu       sync.RWMutex
	projects map[string]project.Project
	sessions map[string]session.Session
	presets  map[string]struct{}

	// Cached read views, rebuilt lazily on the first read after a write
	// (nil = stale). Sessions() and SessionIDs() are on every dashboard
	// request; without the cache each call re-copied the whole map and
	// re-sorted it, which is O(all sessions) work for a page that shows 50.
	// The maps/slices handed out are REPLACED on rebuild, never mutated, so
	// callers may hold them across the lock — but must treat them as
	// read-only.
	viewIDs []string                   // session ids, LastActive descending
	viewMap map[string]session.Session // shared snapshot

	// adopted holds sessions this process learned about from disk rather
	// than from its own mutators — see adoptNewSessionsLocked. They are
	// owned by another process, so their cached copy goes stale on its
	// own and is re-read on each scan. lastDiskScan rate-limits that scan.
	adopted      map[string]struct{}
	lastDiskScan time.Time
}

// diskScanInterval bounds how often a listing read re-scans the sessions
// directory for folders this process has never seen. One ReadDir per
// interval is cheap next to a dashboard that silently omits live work.
const diskScanInterval = 2 * time.Second

// invalidateSessionViews marks the cached views stale. Callers must hold
// the write lock.
func (r *Registry) invalidateSessionViews() {
	r.viewIDs, r.viewMap = nil, nil
}

// New returns an empty registry bound to the given layout. Call
// Reload() before serving traffic.
func New(layout config.Layout) *Registry {
	return &Registry{
		layout:   layout,
		projects: map[string]project.Project{},
		sessions: map[string]session.Session{},
		presets:  map[string]struct{}{},
	}
}

// Layout returns the underlying layout. Read-only.
func (r *Registry) Layout() config.Layout { return r.layout }

// Reload re-scans every folder and rebuilds the in-memory maps.
// Designed to be safe to call at any time: takes the write lock,
// replaces maps wholesale. On boot, also resets each session's status
// to idle (any subprocess from the previous run is dead).
func (r *Registry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.layout.EnsureLayout(); err != nil {
		return err
	}

	presetNames, err := preset.List(r.layout)
	if err != nil {
		return err
	}
	presets := make(map[string]struct{}, len(presetNames))
	for _, n := range presetNames {
		presets[n] = struct{}{}
	}

	projectIDs, err := project.List(r.layout)
	if err != nil {
		return err
	}
	projects := make(map[string]project.Project, len(projectIDs))
	for _, id := range projectIDs {
		p, err := project.Load(r.layout, id)
		if err != nil {
			// Skip unreadable folders rather than fail the whole boot.
			// A broken project shouldn't prevent the rest of wick
			// from running — operator can fix it manually. Logged loudly:
			// a skipped project 404s everywhere with no other trace.
			log.Warn().Err(err).Str("project", id).
				Msg("registry: skipping unreadable project folder")
			continue
		}
		projects[id] = p
	}

	// ListAll, not List: sub-agent sessions are nested inside their
	// parents and would otherwise be invisible to the registry, leaving a
	// restarted wick unable to render or resume them.
	sessionIDs, err := session.ListAll(r.layout)
	if err != nil {
		return err
	}
	sessions := make(map[string]session.Session, len(sessionIDs))
	for _, id := range sessionIDs {
		s, err := session.Load(r.layout, id)
		if err != nil {
			continue
		}
		// Subprocess from previous run is gone — force status to idle
		// and zero per-agent statuses. cli_session_id is preserved for
		// resume.
		dirty := false
		if s.Meta.Status != session.StatusIdle {
			s.Meta.Status = session.StatusIdle
			dirty = true
		}
		for i := range s.Agents {
			if s.Agents[i].Status != "idle" {
				s.Agents[i].Status = "idle"
				dirty = true
			}
		}
		if dirty {
			_ = session.SaveMeta(r.layout, id, s.Meta)
			_ = session.SaveAgents(r.layout, id, s.Agents)
		}
		// Merge any inflight.jsonl left over from the previous process
		// into conversation.jsonl as a truncated assistant turn. Without
		// this, a subsequent --resume would branch off a history the
		// agent CLI never saw the answer for.
		recoveryAgent := s.Meta.ActiveAgent
		if recoveryAgent == "" && len(s.Agents) > 0 {
			recoveryAgent = s.Agents[0].Name
		}
		recoveryProvider := ""
		for _, a := range s.Agents {
			if a.Name == recoveryAgent {
				recoveryProvider = a.Provider
				break
			}
		}
		if recovered, err := store.RecoverInflight(r.layout, id, recoveryAgent, recoveryProvider, nil); err != nil {
			log.Warn().Err(err).Str("session", id).Msg("registry: recover inflight failed")
		} else if recovered {
			log.Info().Str("session", id).Str("agent", recoveryAgent).Msg("registry: recovered inflight turn into conversation.jsonl")
		}
		sessions[id] = s
	}

	r.projects = projects
	r.sessions = sessions
	r.presets = presets
	r.invalidateSessionViews()
	return nil
}

// Projects returns a snapshot copy of the projects map keyed by id.
func (r *Registry) Projects() map[string]project.Project {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]project.Project, len(r.projects))
	for k, v := range r.projects {
		out[k] = v
	}
	return out
}

// ProjectIDs returns project ids sorted by display name.
func (r *Registry) ProjectIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	type kv struct {
		id   string
		name string
	}
	all := make([]kv, 0, len(r.projects))
	for id, p := range r.projects {
		all = append(all, kv{id, p.Meta.Name})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
	out := make([]string, len(all))
	for i, e := range all {
		out[i] = e.id
	}
	return out
}

// Project returns one project by id, ok=false if missing.
func (r *Registry) Project(id string) (project.Project, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.projects[id]
	return p, ok
}

// Sessions returns a shared read-only snapshot of the sessions map. The
// snapshot is cached between writes: repeated dashboard reads cost a map
// return, not an O(all sessions) copy. Do NOT mutate the result — writes
// replace the snapshot, they never edit it, so a mutation here would
// corrupt every other reader holding it.
func (r *Registry) Sessions() map[string]session.Session {
	r.adoptNewSessions()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebuildSessionViewsLocked()
	return r.viewMap
}

// SessionIDs returns IDs sorted by last_active descending — the order
// listing pages want by default. Cached between writes (see Sessions);
// treat the slice as read-only.
func (r *Registry) SessionIDs() []string {
	r.adoptNewSessions()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebuildSessionViewsLocked()
	return r.viewIDs
}

// rebuildSessionViewsLocked (re)builds the cached snapshot + sorted-id list
// when stale. Callers must hold the write lock. Both views are built
// together: they are two shapes of the same read and always describe the
// same state, so a list page and its meta lookups can never disagree.
func (r *Registry) rebuildSessionViewsLocked() {
	if r.viewMap != nil && r.viewIDs != nil {
		return
	}
	// maps.Clone, not an insert loop: it copies the hash table directly
	// instead of re-hashing every key, which is the bulk of this rebuild
	// once an install holds thousands of sessions.
	snap := maps.Clone(r.sessions)
	if snap == nil {
		snap = map[string]session.Session{}
	}
	// Sort ORDER KEYS, not ids-plus-map-lookups. Sorting ids alone means
	// two map lookups per comparison — ~120k hash lookups at 5k sessions,
	// for an answer that is one int64 per session. Reading the key once
	// and sorting pairs turns the comparator into an integer compare.
	type ordered struct {
		id string
		at int64
	}
	keys := make([]ordered, 0, len(snap))
	for k, v := range snap {
		keys = append(keys, ordered{id: k, at: v.Meta.LastActive.UnixNano()})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].at != keys[j].at {
			return keys[i].at > keys[j].at // LastActive descending
		}
		// Ties (seeded/imported sessions share a timestamp) resolve by id
		// so paging over the list is stable rather than map-order random.
		return keys[i].id < keys[j].id
	})
	ids := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = k.id
	}
	r.viewMap, r.viewIDs = snap, ids
}

// Session returns one session by ID, ok=false if missing.
//
// A cache miss is not proof the session is gone, so it falls through to
// disk before answering no — see adoptNewSessionsLocked for why the
// cache can be behind. Callers that render a shared link (a session
// page, a spawn log) would otherwise tell the reader a session was
// deleted while its folder sits right there.
func (r *Registry) Session(id string) (session.Session, bool) {
	r.mu.RLock()
	s, ok := r.sessions[id]
	r.mu.RUnlock()
	if ok {
		return s, true
	}
	return r.adoptSession(id)
}

// adoptSession loads one session straight from disk and caches it.
// Returns ok=false only when the folder is really unreadable or absent,
// which is the one case that means deleted.
func (r *Registry) adoptSession(id string) (session.Session, bool) {
	if id == "" {
		return session.Session{}, false
	}
	s, err := session.Load(r.layout, id)
	if err != nil {
		return session.Session{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Another reader may have adopted it while we were off the lock;
	// theirs wins so two callers never hold different structs.
	if existing, ok := r.sessions[id]; ok {
		return existing, true
	}
	r.cacheAdoptedLocked(s)
	log.Info().Str("session", id).Msg("registry: adopted session from disk after cache miss")
	return s, true
}

// PresetNames returns sorted preset names.
func (r *Registry) PresetNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.presets))
	for k := range r.presets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasPreset reports whether the registry knows about a preset.
func (r *Registry) HasPreset(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.presets[name]
	return ok
}

// upsert / delete helpers used by Manager (same package). Callers must
// not hold the read lock when invoking these.

func (r *Registry) upsertProject(p project.Project) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projects[p.Meta.ID] = p
}

// adoptNewSessions pulls in sessions that exist on disk but were never
// announced to this process, and refreshes the ones it already adopted.
//
// The cache is filled once at boot by Reload and after that only by this
// process's own mutators — filesystem watching is out of scope (see the
// package doc). That holds while one process owns the directory. A
// zero-downtime reload runs two for the length of the handoff, and every
// session the other process creates in that window is invisible here:
// absent from the conversation list, and reported as deleted by anything
// that reads a cache miss as proof of absence.
//
// So re-scan the sessions directory, at most once per diskScanInterval,
// and adopt what turns up. Adoption is deliberately READ-ONLY, unlike
// Reload: it must not force status to idle or recover inflight turns,
// because the session it adopts may be mid-turn inside the other process
// and those repairs belong to whoever owns the subprocess.
//
// Only top-level sessions are scanned. Sub-agents live inside a parent
// folder and would cost a ReadDir each; they are adopted on demand by
// adoptSession when something looks one up.
//
// The scan and the file reads happen OUTSIDE the lock — a ReadDir over
// thousands of session folders is milliseconds, and holding the write
// lock across it would stall every dashboard read for that long. Only
// the merge at the end takes the lock.
func (r *Registry) adoptNewSessions() {
	if !r.claimDiskScan(time.Now()) {
		return
	}
	ids, err := session.List(r.layout)
	if err != nil {
		log.Warn().Err(err).Msg("registry: scanning sessions dir for new folders failed")
		return
	}

	r.mu.RLock()
	pending := make([]string, 0, 4)
	for _, id := range ids {
		_, known := r.sessions[id]
		_, adopted := r.adopted[id]
		// Ours stay fresh through our own mutators; only a session another
		// process owns needs re-reading for its new status and last_active.
		if !known || adopted {
			pending = append(pending, id)
		}
	}
	onDisk := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		onDisk[id] = struct{}{}
	}
	gone := make([]string, 0, 2)
	for id := range r.adopted {
		if _, still := onDisk[id]; !still && !strings.Contains(id, config.SubSessionSep) {
			gone = append(gone, id)
		}
	}
	r.mu.RUnlock()

	loaded := make([]session.Session, 0, len(pending))
	fresh := make(map[string]bool, len(pending))
	for _, id := range pending {
		s, err := session.Load(r.layout, id)
		if err != nil {
			// A folder mid-creation, or an unreadable one: skip it and let
			// the next scan decide.
			continue
		}
		loaded = append(loaded, s)
		fresh[id] = true
	}
	if len(loaded) == 0 && len(gone) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range loaded {
		if _, known := r.sessions[s.ID]; !known {
			log.Info().Str("session", s.ID).Msg("registry: adopted session created outside this process")
		}
		r.cacheAdoptedLocked(s)
	}
	// An adopted session whose folder is gone was deleted by the process
	// that owns it. Dropping only adopted ids keeps this away from our own
	// sessions, whose folder can briefly lag the cache during create.
	for _, id := range gone {
		if fresh[id] {
			continue // re-created between the scan and here
		}
		delete(r.adopted, id)
		delete(r.sessions, id)
		r.invalidateSessionViews()
	}
}

// claimDiskScan reports whether this caller should run the scan, taking
// the slot if so. Rate limiting has to be a claim rather than a check:
// two concurrent dashboard reads would otherwise both see a stale
// timestamp and both scan.
func (r *Registry) claimDiskScan(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.lastDiskScan.IsZero() && now.Sub(r.lastDiskScan) < diskScanInterval {
		return false
	}
	r.lastDiskScan = now
	return true
}

// cacheAdoptedLocked stores a session read from disk and remembers that
// this process does not own it. Callers must hold the write lock.
func (r *Registry) cacheAdoptedLocked(s session.Session) {
	if r.adopted == nil {
		r.adopted = make(map[string]struct{})
	}
	r.sessions[s.ID] = s
	r.adopted[s.ID] = struct{}{}
	r.invalidateSessionViews()
}

func (r *Registry) upsertSession(s session.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
	r.invalidateSessionViews()
}

func (r *Registry) upsertPreset(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.presets[name] = struct{}{}
}

func (r *Registry) deleteProject(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.projects, id)
}

// deleteSession drops a session and every sub-agent session beneath it.
//
// Deleting a conversation removes its children from disk too — they live
// inside its folder — so leaving them in the map would hand out sessions
// whose transcript is already gone. Descendants are identifiable from the
// id alone: a child's id is its parent's plus a separator and a segment.
func (r *Registry) deleteSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
	prefix := id + config.SubSessionSep
	for sid := range r.sessions {
		if strings.HasPrefix(sid, prefix) {
			delete(r.sessions, sid)
		}
	}
	r.invalidateSessionViews()
}

func (r *Registry) deletePreset(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.presets, name)
}
