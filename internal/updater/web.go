package updater

import (
	"context"
	"sync"
)

// Phase is the current stage of a web-driven update job. The System
// admin page renders each phase differently (spinner, percent bar,
// "ready to apply" button, error banner).
type Phase string

const (
	PhaseIdle        Phase = "idle"        // nothing happening
	PhaseChecking    Phase = "checking"    // hitting GitHub for the latest release
	PhaseDownloading Phase = "downloading" // transferring the asset (Percent meaningful)
	PhaseStaged      Phase = "staged"      // a binary is downloaded and ready to apply
	PhaseUpToDate    Phase = "up-to-date"  // current version is the latest
	PhaseNoAsset     Phase = "no-asset"    // newer release exists but no build for this OS/arch
	PhaseApplying    Phase = "applying"    // swap + re-exec in progress
	PhaseError       Phase = "error"       // last job failed; Error is set
)

// Status is the snapshot the web UI renders. It is the SSE payload and
// the initial page-load state. Percent is only meaningful while Phase
// is PhaseDownloading.
type Status struct {
	Phase          Phase  `json:"phase"`
	Percent        int    `json:"percent"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version,omitempty"`
	HasStaged      bool   `json:"has_staged"`
	StagedVersion  string `json:"staged_version,omitempty"`
	// ReleaseNotes is the latest release's body (markdown) and
	// PublishedAt its RFC3339 date — what changed and when. Set once a
	// check finds a newer (or up-to-date) release; empty until then.
	ReleaseNotes string `json:"release_notes,omitempty"`
	PublishedAt  string `json:"published_at,omitempty"`
	// WantedAsset is the asset filename that was expected but absent when
	// Phase is PhaseNoAsset (a newer release with no build for this OS).
	WantedAsset string `json:"wanted_asset,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Coordinator owns the single in-flight web update job and fans its
// status out to any number of SSE subscribers. One job at a time: a
// second Check while one is running is a no-op. Safe for concurrent
// use.
//
// It wraps an *Updater (the actual download/stage/apply machinery) so it
// can compose a full Status without reaching into the updater's private
// fields.
type Coordinator struct {
	upd *Updater

	mu       sync.Mutex
	status   Status
	inFlight bool
	subs     map[chan Status]struct{}
}

// NewCoordinator builds a Coordinator around upd. currentVersion is the
// running binary's version (e.g. app.BuildAppVersion), shown verbatim
// on the System page. upd may be a non-Configured updater — the page
// then renders a "not configured" state and never calls Check.
func NewCoordinator(upd *Updater, currentVersion string) *Coordinator {
	c := &Coordinator{
		upd:  upd,
		subs: make(map[chan Status]struct{}),
	}
	c.status = Status{
		Phase:          PhaseIdle,
		CurrentVersion: currentVersion,
	}
	if upd != nil {
		c.status.HasStaged = upd.HasStaged()
		c.status.StagedVersion = upd.StagedVersion()
		if c.status.HasStaged {
			c.status.Phase = PhaseStaged
		}
	}
	return c
}

// Updater returns the wrapped updater so handlers can call Configured /
// HasStaged / ApplyStagedAndRestart directly.
func (c *Coordinator) Updater() *Updater { return c.upd }

// Snapshot returns the current status. Cheap; safe to call per request.
func (c *Coordinator) Snapshot() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Subscribe registers an SSE listener. The returned channel receives
// the current status immediately and every subsequent update; the
// returned func unsubscribes and closes the channel. The channel is
// buffered and lossy — a slow consumer drops intermediate ticks rather
// than blocking the update job (it always sees the final state).
func (c *Coordinator) Subscribe() (<-chan Status, func()) {
	ch := make(chan Status, 4)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	// Prime with current state inside the lock so the first frame can't be
	// reordered behind a concurrent set() update. Safe to send under the
	// lock: the channel is buffered and freshly created, so it can't block.
	ch <- c.status
	c.mu.Unlock()
	unsub := func() {
		c.mu.Lock()
		if _, ok := c.subs[ch]; ok {
			delete(c.subs, ch)
			close(ch)
		}
		c.mu.Unlock()
	}
	return ch, unsub
}

// set replaces the status and notifies subscribers. Must be called
// without holding the mutex by the caller — it locks internally.
func (c *Coordinator) set(mut func(s *Status)) {
	c.mu.Lock()
	mut(&c.status)
	snap := c.status
	for ch := range c.subs {
		select {
		case ch <- snap:
		default:
			// Slow subscriber — drop this tick. It will get the next one
			// (and the buffered channel still holds the latest pending).
		}
	}
	c.mu.Unlock()
}

// Check runs CheckLatest then, if a newer version exists, downloads and
// stages it while streaming percent progress to subscribers. It is the
// single entry point the web "Check for updates" button triggers. A
// second call while one is in flight returns immediately. Errors land
// in Status.Error with Phase == PhaseError.
func (c *Coordinator) Check(ctx context.Context) {
	c.mu.Lock()
	if c.upd == nil || !c.upd.Configured() || c.inFlight {
		c.mu.Unlock()
		return
	}
	c.inFlight = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFlight = false
		c.mu.Unlock()
	}()

	c.set(func(s *Status) {
		s.Phase = PhaseChecking
		s.Percent = 0
		s.Error = ""
		s.LatestVersion = ""
	})

	info, err := c.upd.CheckLatest(ctx)
	if err != nil {
		c.set(func(s *Status) {
			s.Phase = PhaseError
			s.Error = err.Error()
		})
		return
	}

	// Carry "what changed + when" on every non-error outcome so the UI
	// can show release notes whether we're up to date or about to apply.
	// For the official build, prefer the published changelog site (covers
	// the full range current→latest, one codebase shared with the
	// framework); downstream apps fall back to the per-release GitHub body.
	published := info.PublishedAt
	notes := info.Notes
	if rng := c.upd.ChangelogRange(ctx, c.upd.CurrentVersion(), info.Version); rng != "" {
		notes = rng
	}

	if info.AlreadyLatest {
		c.set(func(s *Status) {
			s.Phase = PhaseUpToDate
			s.LatestVersion = info.Version
			s.ReleaseNotes = notes
			s.PublishedAt = published
		})
		return
	}

	// A newer version exists but there's no build for this OS/arch. Not an
	// error — show the version + notes so the user knows what's available,
	// and the UI recommends building from source or asking the maintainer.
	if info.NoAssetForOS {
		c.set(func(s *Status) {
			s.Phase = PhaseNoAsset
			s.LatestVersion = info.Version
			s.ReleaseNotes = notes
			s.PublishedAt = published
			s.WantedAsset = info.WantedAsset
		})
		return
	}

	// AlreadyStaged or a fresh download both end in PhaseStaged.
	if info.AlreadyStaged {
		c.set(func(s *Status) {
			s.Phase = PhaseStaged
			s.LatestVersion = info.Version
			s.HasStaged = true
			s.StagedVersion = c.upd.StagedVersion()
			s.Percent = 100
			s.ReleaseNotes = notes
			s.PublishedAt = published
		})
		return
	}

	c.set(func(s *Status) {
		s.Phase = PhaseDownloading
		s.LatestVersion = info.Version
		s.Percent = 0
		s.ReleaseNotes = notes
		s.PublishedAt = published
	})

	err = c.upd.DownloadWithProgress(ctx, info, func(done, total int64) {
		pct := 0
		if total > 0 {
			pct = int(done * 100 / total)
			if pct > 100 {
				pct = 100
			}
		}
		c.set(func(s *Status) {
			s.Phase = PhaseDownloading
			s.Percent = pct
		})
	})
	if err != nil {
		c.set(func(s *Status) {
			s.Phase = PhaseError
			s.Error = err.Error()
		})
		return
	}

	c.set(func(s *Status) {
		s.Phase = PhaseStaged
		s.Percent = 100
		s.HasStaged = c.upd.HasStaged()
		s.StagedVersion = c.upd.StagedVersion()
		s.ReleaseNotes = notes
		s.PublishedAt = published
	})
}

// MarkApplying flips the status to PhaseApplying just before the
// handler triggers ApplyStagedAndRestart (which never returns on
// success). It lets any connected SSE client paint "Restarting…" before
// the process re-execs and the stream drops.
func (c *Coordinator) MarkApplying() {
	c.set(func(s *Status) {
		s.Phase = PhaseApplying
	})
}
