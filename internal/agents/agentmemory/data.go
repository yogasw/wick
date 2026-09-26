package agentmemory

import (
	"context"
	"encoding/json"
)

// The panel's data contract. A backend translates its own surface (ai-memory:
// CLI `--json`, three REST endpoints, and the one narrowly-allowed MCP call —
// PLAN §13.2.1) into these types, so the core handlers and wick's FE never
// learn one backend's flag names or JSON shape. Every field below was read off
// a real `ai-memory 2.4.0` response (2026-09-25); nothing here is guessed, and
// what a backend cannot answer it leaves absent rather than filling in.

// Conn is everything a backend needs to reach its store on one data call. The
// core resolves it from the manager + the daemon settings before each call.
type Conn struct {
	// BaseURL is the daemon's loopback base, e.g. "http://127.0.0.1:49374".
	BaseURL string
	// DataDir is the store on disk. Empty = the backend's own default.
	DataDir string
	// AuthToken is the plaintext bearer token, "" when the daemon runs
	// without auth.
	AuthToken string
	// WebEnabled mirrors the daemon's --enable-web. False means the HTTP
	// API is not mounted, which a backend must report as ErrWebDisabled
	// rather than as a mystery 404 (PLAN §13.1).
	WebEnabled bool
}

// ReadScope narrows a project-scoped read. ai-memory resolves the scope from the
// process's cwd when it is not told one, so Dir matters as much as the names:
// a command run from the wrong directory silently answers about another
// project (PLAN §14).
type ReadScope struct {
	Workspace string `json:"workspace,omitempty"`
	Project   string `json:"project,omitempty"`
	// Dir is the working directory the command runs in. Empty = wick's own
	// cwd, which is only correct for store-wide reads.
	Dir string `json:"dir,omitempty"`
}

// DataSource is the read/act surface one backend exposes to the panel. A
// backend that cannot answer a call returns an error; the core turns that into
// a field on the response rather than failing the whole page, so one broken
// probe never blanks the dashboard.
type DataSource interface {
	// StoreStatus reads the store-wide counters, index coverage, ingest and
	// spool state. Store-wide, NOT per project — the two sets of numbers
	// differ and must never be mixed in one card (PLAN §13.5).
	StoreStatus(ctx context.Context, conn Conn) (*StoreStatus, error)
	// Projects lists every workspace/project pair the store knows.
	Projects(ctx context.Context, conn Conn) ([]ProjectRow, error)
	// Health runs the read-only diagnostics: capture coverage and the
	// cross-project contamination audit.
	Health(ctx context.Context, conn Conn, s ReadScope) (*HealthReport, error)
	// Handoffs lists the open cross-agent handoffs in scope.
	Handoffs(ctx context.Context, conn Conn, s ReadScope, limit int) ([]Handoff, error)
	// Search returns wiki hits for q.
	Search(ctx context.Context, conn Conn, s ReadScope, q string) ([]SearchHit, error)
	// Backfill previews (dry run) or performs an import of local harness
	// history. The core never sets Force on its own — see BackfillRequest.
	Backfill(ctx context.Context, conn Conn, req BackfillRequest) (*BackfillReport, error)
}

// Compactor is the optional maintenance half of a backend. It is deliberately
// NOT part of DataSource: everything in DataSource reads, while compaction
// takes an exclusive lock on the store and rewrites it, and a backend that
// cannot do that should answer "not implemented" rather than implement a
// no-op.
type Compactor interface {
	// Compact reclaims free database pages. It deletes nothing, but it
	// blocks every write for as long as it runs — which is why the core
	// gates it behind an explicit confirmation.
	Compact(ctx context.Context, conn Conn) (*CompactReport, error)
}

// CompactReport is one compaction's outcome.
//
// Output is the backend's own line, not parsed numbers. ai-memory's `compact`
// is the one subcommand in this feature with no --json mode (verified against
// ai-memory 2.4.0, 2026-09-25) — it prints
// "Compacted: 1.0 MiB → 1020.0 KiB (24.0 KiB reclaimed)." — so wick carries
// that sentence through instead of inventing a structure the CLI never
// promised. The freed bytes show up on the next status read anyway.
type CompactReport struct {
	Output string `json:"output"`
}

// ── store status ─────────────────────────────────────────────────────

// StoreStatus is the store-wide state behind the Overview + Analytics tabs.
type StoreStatus struct {
	Version     string       `json:"version"`
	DataDir     string       `json:"data_dir"`
	DBPath      string       `json:"db_path"`
	Bind        string       `json:"bind"`
	CaptureMode string       `json:"capture_mode"`
	Counts      StoreCounts  `json:"counts"`
	Index       IndexState   `json:"index"`
	Storage     StorageState `json:"storage"`
	Ingest      IngestState  `json:"ingest"`
	Spool       SpoolState   `json:"spool"`
	// LLM and Embedding say whether recall is running zero-LLM. Zero-LLM is
	// not just "fewer features": long identifiers get truncated out of the
	// stored fact (PLAN §9), which the panel has to be able to warn about.
	LLM       ProviderState `json:"llm"`
	Embedding ProviderState `json:"embedding"`
	// Raw is the backend's untouched JSON, so a field wick has not modelled
	// yet is still reachable from the FE instead of being dropped.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// StoreCounts are the headline numbers. PagesAll counts every version, so it
// runs ahead of PagesLatest — the gap is how much history a compaction would
// reclaim, and a `backfill --force` inflates it (PLAN §11.1).
type StoreCounts struct {
	PagesLatest  int64 `json:"pages_latest"`
	PagesAll     int64 `json:"pages_all"`
	Sessions     int64 `json:"sessions"`
	Observations int64 `json:"observations"`
}

// IndexState is search-index coverage. FTS rows below their table's row count
// means full-text search is blind to the difference; zero embeddings means
// semantic search is off entirely.
type IndexState struct {
	PagesRows               int64 `json:"pages_rows"`
	PagesFTSRows            int64 `json:"pages_fts_rows"`
	ObservationsRows        int64 `json:"observations_rows"`
	ObservationsFTSRows     int64 `json:"observations_fts_rows"`
	EmbeddingRows           int64 `json:"embedding_rows"`
	LatestPagesMissingEmbed int64 `json:"latest_pages_missing_embeddings"`
}

// StorageState is what the store occupies and what a compact would give back.
type StorageState struct {
	DatabaseBytes    int64 `json:"database_bytes"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
	FreeBytes        int64 `json:"data_dir_free_bytes"`
}

// IngestState counts what the server did with the events hooks sent it. A
// rising dropped/shed share means the capture policy is too tight or the
// server is saturated — the difference the Analytics tab exists to show.
type IngestState struct {
	Accepted        int64 `json:"accepted"`
	DroppedByPolicy int64 `json:"dropped_by_policy"`
	ShedSaturated   int64 `json:"shed_saturated"`
	ShedRateLimited int64 `json:"shed_rate_limited"`
	LastPersistedMS int64 `json:"last_persisted_ms,omitempty"`
}

// SpoolState is the hook-side queue. Pending piling up means hooks are firing
// but the server is not absorbing them.
type SpoolState struct {
	Pending      int64 `json:"pending"`
	OldestAgeMS  int64 `json:"oldest_age_ms,omitempty"`
	RetriesTotal int64 `json:"retries_total"`
}

// ProviderState is one model provider's state ("disabled" / "ok" / …).
type ProviderState struct {
	Status   string `json:"status"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Dim      int    `json:"dim,omitempty"`
	LastErr  string `json:"last_error_message,omitempty"`
}

// ── projects, search, handoffs ───────────────────────────────────────

// ProjectRow is one project in the store.
//
// The first four fields are everything the project listing itself returns.
// Briefing carries the per-project counters that listing has no room for, and
// is nil when the backend cannot brief or the call for this one row failed —
// never zeroed, because a project whose numbers could not be read has not
// been shown to have none (PLAN §13.5).
type ProjectRow struct {
	Workspace   string `json:"workspace"`
	Project     string `json:"project"`
	PageCount   int64  `json:"page_count"`
	LastUpdated string `json:"last_updated,omitempty"`

	Briefing *ProjectBriefing `json:"briefing,omitempty"`
	// BriefingError is why this row has no briefing. Per row, not per page:
	// one project that cannot be briefed must not blank the other rows'
	// numbers.
	BriefingError string `json:"briefing_error,omitempty"`
}

// ── per-project briefing ─────────────────────────────────────────────

// ProjectBriefer is the optional per-project counter source. Like Compactor
// it is deliberately NOT part of DataSource: a backend whose surface cannot
// answer "how many sessions does THIS project have" should say so by not
// implementing this, rather than by returning zeros that read as facts.
//
// For ai-memory the only surface that answers it is the MCP `memory_briefing`
// call — allowed for this one call and nothing else (Yoga, 2026-09-25;
// PLAN §13.2.1). It is declared zero-LLM, read-only, and one request per
// project, which is what makes the exception narrow enough to take.
type ProjectBriefer interface {
	// ProjectBriefing reads one project's counters. The scope must name the
	// workspace AND project explicitly — see the note on ReadScope: a call
	// that leaves them empty is answered about whatever project the backend
	// resolves from elsewhere, which is a wrong answer that looks right.
	ProjectBriefing(ctx context.Context, conn Conn, s ReadScope) (*ProjectBriefing, error)
}

// ProjectBriefing is one project's own numbers — the set the project listing
// cannot answer. Every field was read off a real ai-memory 2.4.0 briefing
// (2026-09-25).
type ProjectBriefing struct {
	Counts      BriefingCounts `json:"counts"`
	Activity7d  ActivityWindow `json:"activity_7d"`
	Activity30d ActivityWindow `json:"activity_30d"`
	// LastObservationAt is when this project last learned anything. Empty
	// when it never has.
	LastObservationAt string `json:"last_observation_at,omitempty"`
	PendingHandoffs   int64  `json:"pending_handoff_count"`
	PendingMessages   int64  `json:"pending_message_count"`
	// RecentPages is the project's most recently updated pages, newest
	// first — the "latest pages" list the detail panel shows.
	RecentPages              []RecentPage `json:"recent_pages,omitempty"`
	CrossProjectDependents   int64        `json:"cross_project_dependents"`
	CrossProjectDependencies int64        `json:"cross_project_dependencies"`
}

// BriefingCounts are this project's lifetime counters. PagesAll counts every
// version and so runs ahead of PagesLatest, the same way StoreCounts does —
// but these are ONE project's, and the two sets must never share a card
// without saying which is which (PLAN §13.5).
type BriefingCounts struct {
	PagesLatest  int64 `json:"pages_latest"`
	PagesAll     int64 `json:"pages_all"`
	Sessions     int64 `json:"sessions"`
	Observations int64 `json:"observations"`
	EvidenceRows int64 `json:"evidence_rows"`
}

// ActivityWindow is what happened in the last Days days.
type ActivityWindow struct {
	Days         int   `json:"days"`
	Sessions     int64 `json:"sessions"`
	Observations int64 `json:"observations"`
	PagesUpdated int64 `json:"pages_updated"`
}

// RecentPage is one recently-updated page. Path is the store-relative path
// ("sessions/<uuid>.md"), not a filesystem one.
type RecentPage struct {
	Path      string `json:"path"`
	Title     string `json:"title,omitempty"`
	Kind      string `json:"kind,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// SearchHit is one wiki match. Snippet carries <mark> tags around the matched
// terms, which the FE must render as markup, not as literal text.
type SearchHit struct {
	Workspace string  `json:"workspace,omitempty"`
	Project   string  `json:"project,omitempty"`
	Path      string  `json:"path"`
	Title     string  `json:"title,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	Snippet   string  `json:"snippet,omitempty"`
	Rank      float64 `json:"rank,omitempty"`
}

// Handoff is one open cross-agent baton — the thing that makes "two providers
// working the same project" visible.
type Handoff struct {
	ID          string `json:"id"`
	FromAgent   string `json:"from_agent,omitempty"`
	ToAgent     string `json:"to_agent,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	CreatedAtMS int64  `json:"created_at_ms,omitempty"`
}

// ── health ───────────────────────────────────────────────────────────

// HealthReport is the Health tab's payload: capture coverage plus the
// contamination audit, each carrying its backend's raw JSON so a finding shape
// wick has not modelled is still displayable.
type HealthReport struct {
	Doctor        DoctorReport        `json:"doctor"`
	Contamination ContaminationReport `json:"contamination"`
	// Collisions is wick's OWN check, not the backend's: two wick projects
	// whose folders resolve to one ai-memory project (PLAN §14.3, §18.1). It
	// is filled by the core after the backend has answered, because the
	// backend cannot see it — the overlap is in wick's project layout, and it
	// is visible before a single session has been captured.
	Collisions CollisionCheck `json:"collisions"`
	// Trial is wick's own check too: while one project has opted in, every
	// project that has not is silently NOT recording. That is a deliberate
	// mode, not a fault — but "memory stopped being written here" is
	// otherwise indistinguishable from a broken hook, which is the thing the
	// rest of this report exists to find. So it is stated.
	Trial TrialCheck `json:"trial"`
	// Daemon answers the question the daemon card's badge answers, from the
	// same source, so the two surfaces cannot disagree about whether the
	// backend is running. It is a health finding because the states it
	// reports — two daemons at once, a wedged one, agents spawning with no
	// memory — are faults nothing else on this report would catch.
	Daemon DaemonCheck `json:"daemon"`
}

// TrialCheck reports the per-project trial: who opted in, and therefore who
// went quiet.
type TrialCheck struct {
	Active   bool     `json:"active"`
	Projects []string `json:"projects,omitempty"`
	// Silenced counts the projects left on "follow the instance" while the
	// trial is on — the ones that stopped recording without anybody
	// touching them.
	Silenced int `json:"silenced"`
}

// RunTrialCheck derives the trial state from the policy store. An unwired
// store is not a trial: the feature then behaves exactly as it did before
// per-project policy existed.
func RunTrialCheck() TrialCheck {
	st := policies()
	if st == nil {
		return TrialCheck{}
	}
	all := st.All()
	on := optedIn(all)
	if len(on) == 0 {
		return TrialCheck{}
	}
	// Counted from wick's PROJECT LIST, not from the policy map: a store is
	// free to return only the projects that carry a value, and a project with
	// no value is exactly the one being silenced. Deriving the count from the
	// values would report 0 silenced on the hosts where it matters most.
	silenced := 0
	if projectLister != nil {
		for _, p := range projectLister() {
			if all[p.ID] == PolicyUnset {
				silenced++
			}
		}
	}
	return TrialCheck{Active: true, Projects: on, Silenced: silenced}
}

// DoctorReport answers "a harness ran here — did anything get captured?".
// Uncaptured is the signal: a harness with recent local sessions and nothing
// in the store is a missing hook, which is exactly the half-wired state a
// read-only provider produces (PLAN §7.1).
type DoctorReport struct {
	Workspace  string          `json:"workspace,omitempty"`
	Project    string          `json:"project,omitempty"`
	SinceDays  int             `json:"since_days,omitempty"`
	Rows       json.RawMessage `json:"rows,omitempty"`
	Uncaptured json.RawMessage `json:"uncaptured,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// ContaminationReport is the cross-project audit. It matters more here than
// upstream: wick sessions hold client data, so a page filed under the wrong
// project is a disclosure, not untidiness.
type ContaminationReport struct {
	SessionsMisbucketed int64           `json:"sessions_misbucketed"`
	Findings            json.RawMessage `json:"findings,omitempty"`
	Error               string          `json:"error,omitempty"`
}

// ── backfill ─────────────────────────────────────────────────────────

// BackfillRequest is one import. The two dangerous knobs are deliberate,
// explicit fields rather than defaults:
//
//   - Force is never set by wick on its own. `backfill --force` is NOT
//     idempotent — it re-imports every selected session and observations do
//     not dedupe, so each run adds the same rows again (PLAN §11.1). It is
//     only ever set from an explicit user action.
//   - Session + Force together is the safe pattern that replaces a bare
//     --force: import exactly the sessions the store is missing, one at a
//     time, leaving the ones already captured untouched (PLAN §11.5).
type BackfillRequest struct {
	Scope       ReadScope `json:"scope"`
	DryRun      bool      `json:"dry_run"`
	Force       bool      `json:"force"`
	Session     string    `json:"session,omitempty"`
	MaxSessions int       `json:"max_sessions,omitempty"`
}

// BackfillReport is one import's outcome.
//
// SkippedNonEmpty true with Selected 0 is the normal no-op: the store already
// holds sessions, so an unforced run does nothing. SkippedForCap above zero
// means MaxSessions truncated the history — the reason wick must not inherit
// the backend's default of 25 (PLAN §10.6).
type BackfillReport struct {
	Workspace        string `json:"workspace,omitempty"`
	Project          string `json:"project,omitempty"`
	Selected         int    `json:"selected"`
	ImportedSessions int    `json:"imported_sessions"`
	ImportedEvents   int    `json:"imported_events"`
	SkippedForCap    int    `json:"skipped_for_cap"`
	FailedSessions   int    `json:"failed_sessions"`
	SkippedNonEmpty  bool   `json:"skipped_non_empty"`
	DryRun           bool   `json:"dry_run"`
}
