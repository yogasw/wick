// Mirrors internal/agents/agentmemory — one type per Go struct, same JSON tag
// names. Nothing here is guessed: every field was read off handlers.go,
// data.go, config.go, manager.go and resources.go.

// ── daemon control ───────────────────────────────────────────────────

// Status is the daemon's install + run state. `state` is the single source of
// truth for the badge; the booleans exist for the finer questions ("is it ours
// to restart?").
export type Status = {
  installed: boolean;
  version: string;
  running: boolean;
  // managed true = the process is one wick spawned, so uptime is known and
  // Stop/Restart act on something we own.
  managed: boolean;
  state: "not-installed" | "starting" | "running" | "stopped";
  pref_port: number;
  bound_port: number;
  base_url: string;
  // started_at_ms is the spawn time of a daemon wick is running. Absent for
  // an adopted daemon — its start is not ours to know (see uptimeOf).
  started_at_ms?: number;
};

// Settings are the daemon-level knobs. autostart_locked is derived server-side
// and rendered as a forced-on, disabled control.
export type Settings = {
  data_dir: string;
  port: number;
  enable_web: boolean;
  autostart: boolean;
  autostart_locked: boolean;
  backfill_max_sessions: number;
} & Tuning;

// Tuning is the rest of the daemon's configuration — flattened into Settings
// by the Go side, which embeds it.
//
// It reaches the daemon as AI_MEMORY_* environment on the launch line, never
// by wick rewriting the backend's config.toml, and an UNSET field is not sent
// at all. That is why "" and 0 mean "whatever the config file says" in this
// form rather than "empty" and "zero" — the UI has to say so next to the
// fields where the difference bites (retention above all).
export type Tuning = {
  // A. daemon
  base_path: string;
  log_level: string;
  // B. access & security
  allowed_hosts: string;
  // auth_token arrives MASKED when one is stored (a row of bullets) and is
  // posted back unchanged unless the user types a new one.
  auth_token: string;
  // C. capture & privacy
  capture_mode: string;
  project_strategy: string;
  capture_assistant: boolean;
  no_capture_prompts: boolean;
  sanitize_extra_patterns: string;
  sanitize_allowlist: string;
  hook_rate_per_sec: number;
  hook_rate_burst: number;
  // D. model providers
  llm_provider: string;
  llm_model: string;
  embedding_provider: string;
  embedding_model: string;
  embedding_dim: number;
  max_input_tokens: number;
  max_output_tokens: number;
  auto_improve_require_approval: boolean;
  auto_improve_min_observations: number;
  auto_improve_min_confidence: number;
  auto_improve_max_proposals_per_run: number;
  // E. retention
  observation_retention_days: number;
  observation_prune_batch: number;
  hard_delete_after_days: number;
  cold_threshold: number;
  // F. recall ranking
  reranker: string;
  decay_lambda: number;
  decay_sigma: number;
  decay_mu: number;
  salience_default: number;
  breadth_weight: number;
  // G. backfill
  backfill_auto: boolean;
};

// InstanceRef is one provider instance wired to a backend. capture false =
// recall only: it reads the store and writes nothing back, which is how a
// store stays alive-looking while capturing nothing.
export type InstanceRef = {
  type: string;
  name: string;
  capture: boolean;
  // server_url set = this instance talks to some other daemon, so its numbers
  // are not the ones on this page.
  server_url?: string;
};

// AutostartLock says whether autostart is forced on, and by whom.
export type AutostartLock = {
  locked: boolean;
  used_by?: InstanceRef[];
  reason?: string;
};

// Resources is the footprint: process RSS and store size on disk. The *_known
// flags matter — a 0 with known false means nobody could measure it, NOT that
// it costs nothing.
export type Resources = {
  pid?: number;
  rss_bytes: number;
  rss_known: boolean;
  data_dir?: string;
  data_dir_bytes: number;
  data_dir_known: boolean;
};

// ── store ────────────────────────────────────────────────────────────

// StoreCounts are the headline numbers. pages_all counts every version, so it
// runs ahead of pages_latest.
export type StoreCounts = {
  pages_latest: number;
  pages_all: number;
  sessions: number;
  observations: number;
};

export type IndexState = {
  pages_rows: number;
  pages_fts_rows: number;
  observations_rows: number;
  observations_fts_rows: number;
  embedding_rows: number;
  latest_pages_missing_embeddings: number;
};

export type StorageState = {
  database_bytes: number;
  reclaimable_bytes: number;
  data_dir_free_bytes: number;
};

export type IngestState = {
  accepted: number;
  dropped_by_policy: number;
  shed_saturated: number;
  shed_rate_limited: number;
  last_persisted_ms?: number;
};

export type SpoolState = {
  pending: number;
  oldest_age_ms?: number;
  retries_total: number;
};

// ProviderState is one model provider's state. status "disabled" on `llm` is
// the zero-LLM mode the Overview warns about.
export type ProviderState = {
  status: string;
  provider?: string;
  model?: string;
  dim?: number;
  last_error_message?: string;
};

export type StoreStatus = {
  version: string;
  data_dir: string;
  db_path: string;
  bind: string;
  capture_mode: string;
  counts: StoreCounts;
  index: IndexState;
  storage: StorageState;
  ingest: IngestState;
  spool: SpoolState;
  llm: ProviderState;
  embedding: ProviderState;
  raw?: unknown;
};

// ── the Overview payload ─────────────────────────────────────────────

// BackendInfo is one registered backend, from GET /agentmemory/backends and
// echoed inside Overview. icon is inline SVG inner markup.
export type BackendInfo = {
  id: string;
  name: string;
  blurb: string;
  icon?: string;
  github_url?: string;
  install_kind?: string;
  has_data: boolean;
};

// Overview is GET /agentmemory/<id>/status in one shot.
//
// `daemon` and `store` are deliberately separate and can disagree — a daemon
// can be up with an empty store, and a store can be readable while the daemon
// is down — so a failure to read one never blanks the other (PLAN §13.5).
// WatchdogState is what supervision has DONE to this daemon (PLAN §25).
//
// It is a record of events, not just a state: a watchdog whose only output is
// "everything is fine" cannot be told apart from one that is not running, and
// a daemon quietly restarted forty times a day is a bug somebody has to see.
export type WatchdogState = {
  // watching = this backend is supervised, which is the effective-autostart
  // signal and nothing else. There is no separate watchdog switch.
  watching: boolean;
  restarts: number;
  // hung_restarts is the subset that were wedged rather than dead — a daemon
  // that keeps hanging is a different bug from one that keeps exiting.
  hung_restarts: number;
  last_reason?: "dead" | "hung" | "off";
  last_restart_ms?: number;
  last_error?: string;
  consecutive_failures: number;
  next_attempt_ms?: number;
  // gave_up: the retrying has stopped on purpose, with the reason. Stated
  // rather than silently continued — this host has 2 vCPU.
  gave_up: boolean;
  gave_up_reason?: string;
  // stopped_by_operator is why an otherwise-supervised daemon is left down.
  stopped_by_operator: boolean;
};

export type Overview = {
  backend: BackendInfo;
  daemon: Status;
  settings: Settings;
  resources: Resources;
  used_by?: InstanceRef[] | null;
  autostart_lock: AutostartLock;
  watchdog?: WatchdogState;
  store?: StoreStatus;
  // store_error explains an unreadable store; store_reason names the cause in
  // one machine-checkable token — see DataReason.
  store_error?: string;
  store_reason?: string;
};

// DataReason is the named cause of an empty panel read. These two are the
// states the user can actually fix, which is why the server names them
// instead of leaving prose.
export type DataReason = "web_disabled" | "daemon_not_running" | "";

// TestResult is POST /agentmemory/<id>/test — the "Test connection" probe.
export type TestResult = {
  base_url: string;
  health_path: string;
  ok: boolean;
  error?: string;
  version?: string;
  counts?: StoreCounts;
  data_dir?: string;
  store_error?: string;
};

// ── panel data ───────────────────────────────────────────────────────

// DataFailure is the shape every panel-data endpoint can answer with INSTEAD
// of its payload. The two named reasons come back with HTTP 200 on purpose:
// a web API that is off and a daemon that is down are ordinary, fixable
// states, not crashes, so the FE branches on `reason` rather than on a status
// code (writeDataError in handlers.go).
export type DataFailure = {
  error?: string;
  reason?: DataReason;
  hint?: string;
};

// BriefingCounts are ONE project's lifetime counters — not the store's.
// pages_all counts every version, so it runs ahead of pages_latest.
export type BriefingCounts = {
  pages_latest: number;
  pages_all: number;
  sessions: number;
  observations: number;
  evidence_rows: number;
};

// ActivityWindow is what happened in this project in the last `days` days.
export type ActivityWindow = {
  days: number;
  sessions: number;
  observations: number;
  pages_updated: number;
};

// RecentPage is one recently-updated page. `path` is store-relative
// ("sessions/<uuid>.md"), not a filesystem path.
export type RecentPage = {
  path: string;
  title?: string;
  kind?: string;
  updated_at?: string;
};

// ProjectBriefing is a project's own numbers, from the backend's
// `memory_briefing` — the single MCP call this dashboard is allowed to make
// (Yoga, 2026-09-25; PLAN §13.2.1). Every one of these is PER PROJECT and
// must never be shown beside the Analytics tab's store-wide totals without
// saying which is which (PLAN §13.5 point 1).
export type ProjectBriefing = {
  counts: BriefingCounts;
  activity_7d: ActivityWindow;
  activity_30d: ActivityWindow;
  last_observation_at?: string;
  pending_handoff_count: number;
  pending_message_count: number;
  recent_pages?: RecentPage[] | null;
  cross_project_dependents: number;
  cross_project_dependencies: number;
};

// ProjectRow is one workspace/project pair in the store.
//
// The first four fields are everything the project listing returns; the
// counters that listing has no room for arrive in `briefing`. It is ABSENT,
// not zeroed, when that one call failed for this row — `briefing_error` says
// why — because a project whose numbers could not be read has not been shown
// to have none.
export type ProjectRow = {
  workspace: string;
  project: string;
  page_count: number;
  last_updated?: string;
  briefing?: ProjectBriefing;
  briefing_error?: string;
};

export type ProjectsResponse = DataFailure & { projects?: ProjectRow[] | null };

// DoctorRow is one harness's capture coverage for a project. `captured` is
// what the store holds; `local_recent` is what ran here. uncaptured true with
// a non-zero local_recent is the half-wired state: the harness runs and its
// hook never reports.
export type DoctorRow = {
  agent: string;
  local_total: number;
  local_recent: number;
  captured: number;
  uncaptured: boolean;
};

export type DoctorReport = {
  workspace?: string;
  project?: string;
  since_days?: number;
  rows?: DoctorRow[] | null;
  uncaptured?: string[] | null;
  error?: string;
};

// ContaminationReport is the cross-project audit. `findings` stays untyped:
// the only run available to read off was an empty one, so its row shape is
// unverified and the UI renders whatever keys arrive rather than dropping the
// rows that actually matter.
export type ContaminationReport = {
  sessions_misbucketed: number;
  findings?: unknown[] | null;
  error?: string;
};

// CollisionMember is one wick project caught in a project-name collision.
// has_marker means THIS folder pins its own scope — a folder governed by a
// parent's marker has none, and writing one is the repair.
export type CollisionMember = {
  id: string;
  name: string;
  folder: string;
  has_marker: boolean;
};

// Collision is a set of wick projects that resolve to ONE ai-memory project,
// so each one's memory is recalled into the others'.
export type Collision = {
  workspace: string;
  project: string;
  members: CollisionMember[];
  fixable: boolean;
};

// CollisionCheck is wick's own check, not the backend's.
//
// `checked: false` is NOT "no collisions" — it means wick could not read its
// project list, and the tab has to say so rather than render a clean result.
export type CollisionCheck = {
  checked: boolean;
  reason?: string;
  scanned: number;
  collisions?: Collision[] | null;
};

// TrialCheck is the per-project trial as the Health tab reads it: who opted
// in, and therefore how many projects went quiet without being touched. It is
// a deliberate mode, but from inside a silenced project it is indistinguishable
// from a hook that stopped working — so the report states it.
export type TrialCheck = {
  active: boolean;
  projects?: string[] | null;
  silenced: number;
};

export type HealthReport = DataFailure & {
  doctor?: DoctorReport;
  contamination?: ContaminationReport;
  collisions?: CollisionCheck;
  // trial is wick's own check: while one project has opted in, every project
  // that has not is silently not recording. A deliberate mode, but one that
  // looks exactly like a broken hook from inside an affected project.
  trial?: TrialCheck;
};

// Handoff is one open cross-agent baton.
export type Handoff = {
  id: string;
  from_agent?: string;
  to_agent?: string;
  cwd?: string;
  created_at_ms?: number;
};

export type HandoffsResponse = DataFailure & { handoffs?: Handoff[] | null };

// BackfillReport is one import's outcome. skipped_non_empty true with
// selected 0 is the normal no-op: the store already holds sessions, so an
// unforced run does nothing.
export type BackfillReport = {
  workspace?: string;
  project?: string;
  selected: number;
  imported_sessions: number;
  imported_events: number;
  skipped_for_cap: number;
  failed_sessions: number;
  skipped_non_empty: boolean;
  dry_run: boolean;
};

// BackfillRequestEcho is what the server actually ran, echoed back beside the
// report.
//
// It is not decoration: `max_sessions` is RESOLVED server-side (wick's 2000
// rather than the backend's 25 when the form sends nothing), so this is the
// only place the cap that truncated an import is visible. It was being sent
// and not declared here — found by the payload sweep in
// internal/agents/agentmemory/handlers_e2e_test.go, 2026-09-25.
export type BackfillRequestEcho = {
  scope: Scope;
  dry_run: boolean;
  force: boolean;
  session?: string;
  max_sessions?: number;
};

export type BackfillResponse = DataFailure & {
  report?: BackfillReport;
  request?: BackfillRequestEcho;
  // warning is the server's own sentence about what a forced import costs —
  // shown verbatim so the UI cannot soften it.
  warning?: string;
};

export type CompactResponse = DataFailure & {
  report?: { output: string };
  warning?: string;
};

// ── wiki ─────────────────────────────────────────────────────────────

// Page is one wiki page with its body, from GET <p>/page.
//
// No `kind` and no `updated_at`: the backend's read-page document does not
// carry them. The search hit that led here does — the Wiki tab shows the hit's
// values as the hit's, not as the page's.
export type Page = {
  path: string;
  workspace?: string;
  project?: string;
  title?: string;
  body: string;
  // frontmatter keys differ per page kind, so it stays an open record.
  frontmatter?: Record<string, unknown>;
};

export type PageResponse = DataFailure & { page?: Page | null };

export type SearchResponse = DataFailure & {
  hits?: SearchHit[] | null;
  query?: string;
  // note explains the whole-token match rule, which is the difference
  // between "my memory is empty" and "my query missed".
  note?: string;
};

// SearchHit is one wiki match. `snippet` carries <mark> tags around the
// matched terms.
export type SearchHit = {
  workspace?: string;
  project?: string;
  path: string;
  title?: string;
  kind?: string;
  snippet?: string;
  rank?: number;
};

// ── handoffs & messages ──────────────────────────────────────────────

// Message is one cross-project message.
//
// The sender is `from_project_id` — a UUID, with no name for it anywhere in
// the backend's surface. The UI shows the id. Anything else would be a guess
// about which project sent it.
export type Message = {
  id: string;
  subject?: string;
  body?: string;
  from_agent?: string;
  from_workspace_id?: string;
  from_project_id?: string;
  state?: string;
  created_at?: string;
  claimed_at?: string;
};

export type MessagesResponse = DataFailure & { messages?: Message[] | null; box?: string };

// HandoffCancelResult is POST <p>/handoffs/cancel.
//
// `cancelled: false` with no error is an ORDINARY answer: the baton was
// already gone. It must never be rendered as a success — see cancelOutcome.
export type HandoffCancelResult = {
  handoff_id: string;
  cancelled: boolean;
  state?: string;
};

export type HandoffCancelResponse = DataFailure & {
  result?: HandoffCancelResult;
  warning?: string;
};

// ── editing one project's memory (PLAN §22) ──────────────────────────

// PageWriteResult is one saved page. `output` is the backend's own line —
// "✓ wrote notes/foo.md (page_id=01a0d8dd) under wick/demo" — kept because it
// names the scope the write actually landed in, which is the one thing worth
// reading after an edit.
export type PageWriteResult = {
  path: string;
  page_id?: string;
  output?: string;
};

export type PageWriteResponse = DataFailure & {
  result?: PageWriteResult;
  // note is the server's statement that every write is committed and so
  // restorable. It comes from the server because it is a claim about the
  // BACKEND's behaviour, not a reassurance the UI made up.
  note?: string;
};

// PageDeleteResult names what was removed. The path is echoed rather than
// assumed: a delete that resolved to another scope is exactly what has to be
// visible.
export type PageDeleteResult = {
  path: string;
  deleted: boolean;
  output?: string;
};

export type PageDeleteResponse = DataFailure & {
  result?: PageDeleteResult;
  warning?: string;
};

// Checkpoint is one commit in the wiki's git history — the way back from a
// bad edit. `time` is unix SECONDS, as git reports them.
export type Checkpoint = {
  oid: string;
  short_oid: string;
  time: number;
  summary: string;
};

export type CheckpointsResponse = DataFailure & { checkpoints?: Checkpoint[] | null };

// RestoreResult includes the checkpoint the restore itself created, because
// undoing an undo is the next thing someone wants.
export type RestoreResult = {
  path: string;
  page_id?: string;
  restored_from?: string;
  pre_checkpoint?: string;
  checkpoint?: string;
};

export type RestoreResponse = DataFailure & {
  result?: RestoreResult;
  note?: string;
};

// ── the per-project switch ───────────────────────────────────────────

// ProjectPolicy is whether a project uses Agent Memory at all, and why.
//
// `value` is what the project itself says; `allowed` is the answer after the
// whole rule is applied, and the two differ on purpose: while any project has
// opted in, the ones that have not are off, which is what makes trialling the
// feature on ONE project possible without a second global switch.
export type ProjectPolicy = {
  value: "" | "on" | "off";
  allowed: boolean;
  trial_mode: boolean;
  trial_projects?: string[] | null;
  reason: string;
};

// ProjectPolicyRow is one project's memory state on the host-wide roster: is
// it recording, and if not, why not.
//
// The id AND the name, always: the id is what the policy is keyed by, the
// name is what a person recognises, and a trial reported as a list of uuids
// is one nobody can act on.
export type ProjectPolicyRow = {
  id: string;
  name: string;
  value: "" | "on" | "off";
  recording: boolean;
  reason: string;
};

// ProjectPolicyRoster answers the question a per-project switch with a
// host-wide consequence forces: which projects are recording, and which went
// quiet because of the trial. A project switched explicitly OFF is in
// neither list — that was a decision, not a side effect.
export type ProjectPolicyRoster = {
  trial: TrialCheck;
  recording: ProjectPolicyRow[];
  silenced: ProjectPolicyRow[];
};

// ── retention sweep ──────────────────────────────────────────────────

// SweepReport is one retention sweep. Like compact, the backend has no --json
// mode for it, so `output` is its own sentence carried through verbatim.
export type SweepReport = { dry_run: boolean; output: string };

export type SweepResponse = DataFailure & { report?: SweepReport; warning?: string };

// Scope narrows a project-scoped read to one workspace/project.
export type Scope = { workspace?: string; project?: string };

// ProjectScope is the server's answer to "which memory bucket does this wick
// project use?" — what the project menu's Agent Memory entry opens onto
// (PLAN §22).
//
// Every field of it comes from the server. The FE never builds a scope name
// from a project name or id: the mapping lives next to the marker writer that
// pins it, and a second copy here would drift silently (PLAN §22.2).
export type ProjectScope = {
  project_id: string;
  // name and folder are the wick project's own, for naming what was opened.
  name: string;
  folder: string;
  workspace: string;
  project: string;
  // source says how the bucket was decided:
  //   marker   — a .ai-memory.toml governs the folder; this is live truth.
  //   wick     — no marker yet, wick will write this one on the next session.
  //   basename — a custom-path folder wick never marks, so the backend
  //              derives the project from the folder NAME, which is the one
  //              case that can collide with another project.
  source: "marker" | "wick" | "basename";
};

// Tab is the panel's tab strip. All seven are built: Overview (4A), Projects,
// Analytics and Health (4B), Wiki, Handoffs and Settings (4C).
export type Tab = "overview" | "projects" | "analytics" | "wiki" | "handoffs" | "health" | "settings";
