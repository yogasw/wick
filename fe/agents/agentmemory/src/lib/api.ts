import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type {
  AutostartLock,
  BackendInfo,
  BackfillResponse,
  CompactResponse,
  HandoffCancelResponse,
  HandoffsResponse,
  HealthReport,
  InstanceRef,
  MessagesResponse,
  Overview,
  PageResponse,
  ProjectsResponse,
  Scope,
  SearchResponse,
  Settings,
  Status,
  SweepResponse,
  TestResult,
} from "./types.js";

// The panel's endpoints hang off the agents tool base (e.g. /tools/agents),
// under /agentmemory/<id>/* — one backend per id. There is no proxy mount
// anywhere: nothing of the backend's own server is exposed at the wick root,
// so every number on this page comes through these handlers (PLAN §13.2).
//
// Writes carry their parameters in the QUERY STRING, not a body. The Go
// handlers read them with c.Form(...), which is http.Request.FormValue and so
// also reads the URL query — the same trick airouter's client uses, and it
// keeps the JSON client happy (no body sent).
//
// Per the fe-module contract these expose the Effect (no layer provided); the
// consumer runs them with WickClientLayer, tests swap in a mock HttpClient.

// fetchBackends lists every registered backend so the switcher can enumerate
// them before it knows which one is selected.
export const fetchBackends = (base: string) =>
  apiGetE<{ backends: BackendInfo[] }>(`${base}/agentmemory/backends`);

// fetchOverview is the Overview tab in one request: daemon, settings,
// resources, the instances using it, and the store counters.
export const fetchOverview = (base: string, id: string) =>
  apiGetE<Overview>(`${base}/agentmemory/${id}/status`);

export const fetchLogs = (base: string, id: string) =>
  apiGetE<{ logs: string }>(`${base}/agentmemory/${id}/logs`);

export const start = (base: string, id: string) =>
  apiPostE<Status>(`${base}/agentmemory/${id}/start`);

export const stop = (base: string, id: string) =>
  apiPostE<Status>(`${base}/agentmemory/${id}/stop`);

export const restart = (base: string, id: string) =>
  apiPostE<Status>(`${base}/agentmemory/${id}/restart`);

// install is wired but the backend answers 501 today — ai-memory is a GitHub
// release asset wick does not fetch yet, so the user puts the binary on PATH.
export const install = (base: string, id: string) =>
  apiPostE<{ output: string }>(`${base}/agentmemory/${id}/install`);

// testConnection probes the daemon's health path and, when it answers, reads
// the store — so a green result carries a version and counts rather than a
// bare dot (PLAN §18.3).
export const testConnection = (base: string, id: string) =>
  apiPostE<TestResult>(`${base}/agentmemory/${id}/test`);

// SettingsResponse is what both GET and POST /settings answer with.
// restart_pending is only on the POST: settings land on the next start, so the
// page must say so rather than imply the running daemon already moved.
export type SettingsResponse = {
  settings: Settings;
  used_by?: InstanceRef[] | null;
  autostart_lock: AutostartLock;
  default_port?: number;
  restart_pending?: boolean;
};

export const fetchSettings = (base: string, id: string) =>
  apiGetE<SettingsResponse>(`${base}/agentmemory/${id}/settings`);

// saveSettings posts every field, including the false/empty ones — a form
// that only sent non-zero values could never clear a store path or switch the
// web API off.
export const saveSettings = (base: string, id: string, s: Settings) =>
  apiPostE<SettingsResponse>(`${base}/agentmemory/${id}/settings?${settingsQuery(s)}`);

// settingsQuery serialises the settings form the way the Go handler reads it.
//
// EVERY field is sent, including the false and empty ones: the handler falls
// back to the stored value for anything absent, so a form that only posted
// what the user touched could never clear a store path, switch the web API
// off, or turn assistant capture back off once it was on.
//
// Two deliberate exceptions:
//   - autostart_locked is derived from the provider instances on every read.
//     Posting it back would let a stale page argue with the toggles that
//     actually hold the lock.
//   - auth_token is posted as it was received. When a token is stored the
//     server sends bullets, and the server keeps the stored value for
//     anything containing one — so round-tripping the mask is what leaves an
//     unedited credential alone.
export function settingsQuery(s: Settings): string {
  const q = new URLSearchParams();
  const str = (k: keyof Settings) => q.set(k, String(s[k] ?? ""));
  const num = (k: keyof Settings) => q.set(k, String(s[k] ?? 0));
  const bool = (k: keyof Settings) => q.set(k, s[k] ? "true" : "false");

  q.set("data_dir", s.data_dir ?? "");
  num("port");
  bool("enable_web");
  bool("autostart");
  num("backfill_max_sessions");

  str("base_path");
  str("log_level");

  str("allowed_hosts");
  str("auth_token");

  str("capture_mode");
  str("project_strategy");
  bool("capture_assistant");
  bool("no_capture_prompts");
  str("sanitize_extra_patterns");
  str("sanitize_allowlist");
  num("hook_rate_per_sec");
  num("hook_rate_burst");

  str("llm_provider");
  str("llm_model");
  str("embedding_provider");
  str("embedding_model");
  num("embedding_dim");
  num("max_input_tokens");
  num("max_output_tokens");
  bool("auto_improve_require_approval");
  num("auto_improve_min_observations");
  num("auto_improve_min_confidence");
  num("auto_improve_max_proposals_per_run");

  num("observation_retention_days");
  num("observation_prune_batch");
  num("hard_delete_after_days");
  num("cold_threshold");

  str("reranker");
  num("decay_lambda");
  num("decay_sigma");
  num("decay_mu");
  num("salience_default");
  num("breadth_weight");

  bool("backfill_auto");
  return q.toString();
}

// ── panel data ───────────────────────────────────────────────────────

// scopeQuery serialises a workspace/project scope. Both keys are omitted when
// empty rather than sent blank: the Go side treats a blank project as
// "store-wide", and an accidental empty string would silently widen a read
// that was meant to be about one project.
export function scopeQuery(scope?: Scope): string {
  const q = new URLSearchParams();
  if (scope?.workspace) q.set("workspace", scope.workspace);
  if (scope?.project) q.set("project", scope.project);
  const s = q.toString();
  return s ? `?${s}` : "";
}

// fetchProjects lists the store's projects. It can answer a DataFailure
// instead of a list — /api/v1/projects only exists with the daemon's web API
// on, and that is a setting away, not a crash (PLAN §13.1).
export const fetchProjects = (base: string, id: string) =>
  apiGetE<ProjectsResponse>(`${base}/agentmemory/${id}/projects`);

// fetchHealth runs the diagnostics. It is deliberately NOT polled: doctor
// walks the local harness stores and the audit scans the whole store, so this
// fires when the Health tab is opened or refreshed by hand (PLAN §13.5.7).
export const fetchHealth = (base: string, id: string, scope?: Scope) =>
  apiGetE<HealthReport>(`${base}/agentmemory/${id}/health${scopeQuery(scope)}`);

export const fetchHandoffs = (base: string, id: string, scope?: Scope) =>
  apiGetE<HandoffsResponse>(`${base}/agentmemory/${id}/handoffs${scopeQuery(scope)}`);

// BackfillOptions mirrors the Go BackfillRequest's dangerous half. `force` and
// `confirm` are separate because the server refuses a forced run that does not
// carry both — the fence is on the endpoint, not only in this client.
export type BackfillOptions = Scope & {
  force?: boolean;
  confirm?: boolean;
  session?: string;
  max_sessions?: number;
};

export function backfillQuery(o: BackfillOptions): string {
  const q = new URLSearchParams(scopeQuery(o).replace(/^\?/, ""));
  if (o.force) q.set("force", "true");
  if (o.confirm) q.set("confirm", "true");
  if (o.session) q.set("session", o.session);
  if (o.max_sessions && o.max_sessions > 0) q.set("max_sessions", String(o.max_sessions));
  const s = q.toString();
  return s ? `?${s}` : "";
}

// previewBackfill is the dry run. It never sets force — a preview that needed
// the dangerous flag to be honest would be no preview at all.
export const previewBackfill = (base: string, id: string, o: BackfillOptions) =>
  apiPostE<BackfillResponse>(`${base}/agentmemory/${id}/backfill/preview${backfillQuery({ ...o, force: false, confirm: false })}`);

export const runBackfill = (base: string, id: string, o: BackfillOptions) =>
  apiPostE<BackfillResponse>(`${base}/agentmemory/${id}/backfill/run${backfillQuery(o)}`);

// ── wiki ─────────────────────────────────────────────────────────────

// searchWiki finds pages. It answers a note about whole-token matching
// alongside the hits, which the tab shows rather than letting the user
// conclude their memory is empty when their query simply missed.
export const searchWiki = (base: string, id: string, q: string, scope?: Scope) => {
  const params = new URLSearchParams(scopeQuery(scope).replace(/^\?/, ""));
  params.set("q", q);
  return apiGetE<SearchResponse>(`${base}/agentmemory/${id}/search?${params.toString()}`);
};

// readPage fetches one page in full. The path is addressed exactly — the
// backend's other lookup form runs a search and returns the top hit, which
// would answer with a different page and look like a successful read.
export const readPage = (base: string, id: string, path: string, scope?: Scope) => {
  const params = new URLSearchParams(scopeQuery(scope).replace(/^\?/, ""));
  params.set("path", path);
  return apiGetE<PageResponse>(`${base}/agentmemory/${id}/page?${params.toString()}`);
};

// ── handoffs & messages ──────────────────────────────────────────────

export const fetchMessages = (base: string, id: string, scope: Scope, box: "inbox" | "outbox") => {
  const params = new URLSearchParams(scopeQuery(scope).replace(/^\?/, ""));
  params.set("box", box);
  return apiGetE<MessagesResponse>(`${base}/agentmemory/${id}/messages?${params.toString()}`);
};

// cancelHandoff retires ONE baton. confirm=true is always sent because the
// only caller is a ConfirmDialog that already named what is lost; the server
// refuses the call without it either way.
//
// The backend's `--expire-all` has no client here on purpose (PLAN §20.2): one
// click that drops every open baton, including ones other agents are waiting
// on, has no honest confirmation text.
export const cancelHandoff = (base: string, id: string, scope: Scope, handoffID: string) => {
  const params = new URLSearchParams(scopeQuery(scope).replace(/^\?/, ""));
  params.set("id", handoffID);
  params.set("confirm", "true");
  return apiPostE<HandoffCancelResponse>(`${base}/agentmemory/${id}/handoffs/cancel?${params.toString()}`);
};

// ── retention ────────────────────────────────────────────────────────

// runSweep previews or performs the retention sweep. Preview and the real run
// share a path because they share every failure mode; only dry_run differs,
// and the real one carries the confirmation the server demands.
export const runSweep = (base: string, id: string, scope: Scope, dryRun: boolean) => {
  const params = new URLSearchParams(scopeQuery(scope).replace(/^\?/, ""));
  params.set("dry_run", dryRun ? "true" : "false");
  if (!dryRun) params.set("confirm", "true");
  return apiPostE<SweepResponse>(`${base}/agentmemory/${id}/forget-sweep?${params.toString()}`);
};

// compactStore reclaims free database pages. confirm=true is always sent
// because the only caller is a ConfirmDialog that already named the cost; the
// server still refuses a call without it.
export const compactStore = (base: string, id: string) =>
  apiPostE<CompactResponse>(`${base}/agentmemory/${id}/compact?confirm=true`);
