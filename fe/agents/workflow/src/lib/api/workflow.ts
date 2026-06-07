import { apiGet, apiPost, apiDelete } from "./client";
import type { Workflow, WorkflowVersion } from "$lib/types/workflow";

const BASE = "/tools/agents";

export type WorkflowSummary = {
  id: string;
  name: string;
  enabled: boolean;
  has_draft: boolean;
  version?: number;
  created_at?: string;
  updated_at?: string;
};

export type WorkflowState = {
  approved?: boolean;
  approved_by?: string;
  approved_at?: string;
  approved_version?: number;
  content_hash?: string;
  governance_mode?: string;
};

export type WorkflowGetResponse = {
  workflow: Workflow;
  draft?: Workflow;
  has_draft: boolean;
  state?: WorkflowState;
};

// ValidationIssue mirrors the parse.Issue shape the backend hands back
// (Path / Message capitalised by Go's json default tags). Severity
// isn't a server field — it comes from the bucket the issue landed in
// (errors[] vs warnings[]) and we tag it at unmarshal time so downstream
// code can iterate one list.
export type ValidationIssue = {
  Path: string;
  Message: string;
  severity?: "error" | "warning";
  // Convenience: derived from Path on the FE so existing code that
  // wants a node id can ask for `.node` instead of regex-parsing.
  node?: string;
  field?: string;
  hint?: string;
};

// Matches Go-side validationPayload(): the v1 templ /save endpoint
// and the JSON /api/workflows/validate + /api/workflows/save endpoints
// all return this shape now.
export type ValidationReport = {
  ok: boolean;
  errors: ValidationIssue[];
  warnings: ValidationIssue[];
  by_node?: Record<string, string[]>;
  global?: string[];
};

export type SaveResponse = {
  ok: boolean;
  validation?: ValidationReport;
  error?: string;
};

// Mirror of pkg/entity/Config — the JSON shape Go marshals when the
// registry API embeds a MatchSchema or op input schema. Field
// casing reflects entity.Config's struct field names verbatim; the
// json:"..." tags only kick in for the presentation-hint fields
// (visible_when, hidden, env_override, col_options).
//
// `Options` syntax depends on `Type`:
//   - dropdown → "a|b|c"          (pipe-separated option values)
//   - kvlist   → "col1|col2"      (pipe-separated column names)
//   - picker   → "<source>"       (LookupProvider source key)
//
// `visible_when` predicate: "<otherField>:<value>" or
// "<otherField>:<v1>|<v2>|<v3>" — show this row only while the
// referenced field's current value is one of those literals.
export type CatalogConfigField = {
  Owner?: string;
  Key: string;
  Value?: string;
  Type?: string;
  Options?: string;
  IsSecret?: boolean;
  CanRegenerate?: boolean;
  Locked?: boolean;
  Required?: boolean;
  Description?: string;
  hidden?: boolean;
  visible_when?: string;
  env_override?: string;
  col_options?: Record<string, string>;
  // mode locks the Fixed/Expression toggle: "" = free (defaults to fixed),
  // "fixed"/"expression" = toggle greyed out, forced to that mode.
  mode?: "fixed" | "expression";
};

export type ChannelEventDescriptor = {
  id: string;
  name: string;
  description: string;
  match_schema?: CatalogConfigField[];
};

export type ChannelOpDescriptor = {
  id: string;
  description?: string;
  destructive?: boolean;
  args_schema?: CatalogConfigField[];
};

export type ChannelDescriptor = {
  name: string;
  supports_session: boolean;
  ops?: ChannelOpDescriptor[];
  events?: ChannelEventDescriptor[];
};

export type ConnectorOpDescriptor = {
  id: string;
  name: string;
  description?: string;
  destructive?: boolean;
  input?: { key: string; description: string; required: boolean }[];
  args_schema?: CatalogConfigField[];
};

export type ConnectorDescriptor = {
  module: string;
  name: string;
  ops?: ConnectorOpDescriptor[];
};

export type CatalogResponse = {
  node_types: { type: string; description: string; when_to_use?: string; schema?: Record<string, unknown>; example?: string }[];
  trigger_types: { type: string; label: string; description: string }[];
  channels: ChannelDescriptor[];
  connectors: ConnectorDescriptor[];
  providers: { name: string; is_default: boolean }[];
};

// Mirror of internal/tools/agents/spa_palette.go. The backend is the
// single source of truth for category + label + badge + drill structure;
// the FE just iterates and renders.
export type PaletteDrag =
  | { type: "node"; node_type: string; channel?: string; module?: string; op?: string }
  | { type: "trigger"; trigger_type: string }
  | { type: "channel-trigger"; channel: string; event: string };

export type PaletteItem = {
  kind: "drag" | "drill";
  label: string;
  badge?: string;
  description?: string;
  drag?: PaletteDrag;
  drill_key?: string;
};

export type PaletteCategory = {
  key: string;
  title: string;
  items: PaletteItem[];
};

export type PaletteResponse = {
  categories: PaletteCategory[];
  drills: Record<string, PaletteItem[]>;
};

// Mirror of wftest.Case / Input / Assertion (internal/agents/workflow/wftest).
// Subjects use dotted paths like "nodes.<id>.output.<field>" or "trace.<idx>.status".
// Operators: equals, not_equals, contains, not_contains, exists, not_exists,
//            gt, gte, lt, lte, matches.
export type TestAssertion = {
  subject: string;
  operator: string;
  value?: unknown;
};

export type TestCaseInput = {
  Event?: Record<string, unknown>;
  Node?: Record<string, unknown>;
};

export type TestCase = {
  name: string;
  input: TestCaseInput;
  expected_output?: Record<string, unknown>;
  assertions: TestAssertion[];
};

export type TestRunResult = {
  name: string;
  pass: boolean;
  failures: string[];
  node_output: Record<string, unknown>;
  duration_ms: number;
};

export type RunSummary = {
  // Backend field is `id`; legacy callers (and the Executions panel)
  // still reference `run_id`. Keep both readable — the API stub
  // populates `run_id` from `id` on the way through.
  id: string;
  run_id: string;
  status: string;
  started_at: string;
  ended_at?: string;
  finished_at?: string;
  // Provenance — `source` is the API surface that fired it ("spa" =
  // editor button, "test" = wftest, "" = automation router). The FE
  // collapses these into a Kind pill (manual / automation / test).
  source?: string;
  trigger_id?: string;
  trigger_type?: string;
  error?: string;
};

export type WorkflowsRegistry = {
  workflows: WorkflowSummary[];
};

// Routes mounted by internal/tools/agents/spa_workflows.go. JSON-only.
export const workflowAPI = {
  list: (): Promise<WorkflowsRegistry> => apiGet(`${BASE}/api/workflows/list`),

  templates: (): Promise<{ templates: { value: string; label: string; desc: string }[] }> =>
    apiGet(`${BASE}/api/workflows/templates`),

  create: (body: { name: string; template?: string }): Promise<{ id: string; name: string }> =>
    apiPost(`${BASE}/api/workflows/create`, body),

  importWorkflow: (body: Workflow): Promise<{ id: string; name: string }> =>
    apiPost(`${BASE}/api/workflows/import`, body),

  duplicate: (id: string): Promise<{ id: string; name: string }> =>
    apiPost(`${BASE}/api/workflows/duplicate/${encodeURIComponent(id)}`, {}),

  get: (id: string): Promise<WorkflowGetResponse> =>
    apiGet(`${BASE}/api/workflows/get/${encodeURIComponent(id)}`),

  saveDraft: (id: string, body: { yaml: string } | Workflow): Promise<SaveResponse> =>
    // Backend accepts both shapes: {yaml: "..."} envelope OR raw
    // Workflow JSON (see normaliseWorkflowBody on the Go side).
    // Response carries `validation` so save + validate land in one
    // round-trip — match v1's templ contract.
    apiPost(`${BASE}/api/workflows/save/${encodeURIComponent(id)}`, body),

  publish: (id: string, _message?: string): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/publish/${encodeURIComponent(id)}`, {}),

  discardDraft: (id: string): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/discard/${encodeURIComponent(id)}`, {}),

  toggle: (id: string, enabled: boolean): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/toggle/${encodeURIComponent(id)}`, { enabled }),

  // Dedicated lock endpoint — skips normal validation + works even
  // when the workflow is already locked (otherwise you couldn't
  // unlock once locked). FE Canvas calls this directly instead of
  // round-tripping through autosave.
  setLock: (id: string, locked: boolean): Promise<{ ok: boolean; locked: boolean }> =>
    apiPost(`${BASE}/api/workflows/lock/${encodeURIComponent(id)}`, { locked }),

  runNow: (
    id: string,
    triggerID: string,
  ): Promise<{ ok: boolean }> =>
    // trigger_id is required server-side. Pick one before calling —
    // the editor pins from workflow.triggers[] before firing so the
    // engine routes to the correct entry_node.
    apiPost(`${BASE}/api/workflows/run/${encodeURIComponent(id)}`, { trigger_id: triggerID }),

  runs: async (
    id: string,
    opts?: {
      page?: number;
      pageSize?: number;
      status?: "success" | "failed" | "running";
      from?: string; // yyyy-mm-dd
      to?: string;   // yyyy-mm-dd
      q?: string;    // substring of run id
      kind?: "manual" | "automation" | "test"; // provenance bucket
    },
  ): Promise<{ runs: RunSummary[]; page: number; has_more: boolean; total: number }> => {
    const qs = new URLSearchParams();
    if (opts?.page) qs.set("page", String(opts.page));
    if (opts?.pageSize) qs.set("page_size", String(opts.pageSize));
    if (opts?.status) qs.set("status", opts.status);
    if (opts?.from) qs.set("from", opts.from);
    if (opts?.to) qs.set("to", opts.to);
    if (opts?.q) qs.set("q", opts.q);
    if (opts?.kind) qs.set("kind", opts.kind);
    const suffix = qs.toString() ? `?${qs}` : "";
    const res = await apiGet<{ runs: any[]; page: number; has_more: boolean; total?: number }>(
      `${BASE}/api/workflows/runs/${encodeURIComponent(id)}${suffix}`,
    );
    // Normalise: ensure each row has both `id` and `run_id` so panels
    // that wrote `r.run_id` (legacy) still work.
    const runs = (res.runs ?? []).map((r) => ({
      ...r,
      run_id: r.run_id ?? r.id,
      id: r.id ?? r.run_id,
    })) as RunSummary[];
    return { runs, page: res.page, has_more: res.has_more, total: res.total ?? -1 };
  },

  remove: (id: string): Promise<{ ok: boolean }> =>
    // Backend currently exposes delete via the legacy form-mode endpoint
    // (no JSON variant yet). We send POST with no body — handler reads
    // the path param. Falls through to whatever the legacy templ flow
    // does (404 surface back if path wrong).
    apiPost(`${BASE}/workflows/edit/${encodeURIComponent(id)}/delete`, {}),

  // Canvas position ops — backend computes DAG-aware layout and persists
  // to the draft so the result survives page refresh.
  autoLayout: (id: string, nodeIDs?: string[]): Promise<Workflow> =>
    apiPost(`${BASE}/api/workflows/auto-layout/${encodeURIComponent(id)}`, {
      node_ids: nodeIDs ?? [],
    }),

  moveNodes: (
    id: string,
    moves: { node_id: string; x: number; y: number }[],
  ): Promise<Workflow> =>
    apiPost(`${BASE}/api/workflows/move-nodes/${encodeURIComponent(id)}`, { moves }),

  canvasView: (id: string): Promise<{ nodes: any[]; triggers: any[]; ascii: string; stats: any }> =>
    apiGet(`${BASE}/api/workflows/canvas/${encodeURIComponent(id)}`),

  templateTest: (
    id: string,
    body: { template: string; sample_event?: string; context?: string },
    signal?: AbortSignal,
  ): Promise<{ ok: boolean; rendered?: string; error?: string; available_keys?: string[]; hint?: string }> =>
    apiPost(`${BASE}/api/workflows/template-test/${encodeURIComponent(id)}`, body, signal),

  // Bottom-panel content endpoints (Validation / Guard / Tests).
  validate: (id: string): Promise<ValidationReport> =>
    apiGet(`${BASE}/api/workflows/validate/${encodeURIComponent(id)}`),

  guard: (id: string): Promise<{ hits: any[]; ok: boolean }> =>
    apiGet(`${BASE}/api/workflows/guard/${encodeURIComponent(id)}`),

  tests: (id: string): Promise<{ cases: { name: string; assertions: number }[] }> =>
    apiGet(`${BASE}/api/workflows/tests/${encodeURIComponent(id)}`),

  // Case CRUD + run — JSON variants of the legacy templ endpoints.
  // `Input.Event` matches Go workflow.Event verbatim ({ Provider, ChannelID, … }).
  testGet: (
    id: string,
    name: string,
  ): Promise<TestCase> =>
    apiGet(
      `${BASE}/api/workflows/tests/${encodeURIComponent(id)}/${encodeURIComponent(name)}`,
    ),

  testSave: (
    id: string,
    body: TestCase,
  ): Promise<{ ok: boolean; name: string }> =>
    apiPost(`${BASE}/api/workflows/tests/${encodeURIComponent(id)}`, body),

  testRun: (
    id: string,
    name: string,
  ): Promise<TestRunResult> =>
    apiPost(
      `${BASE}/api/workflows/tests/${encodeURIComponent(id)}/${encodeURIComponent(name)}/run`,
      {},
    ),

  testDelete: (
    id: string,
    name: string,
  ): Promise<{ ok: boolean }> =>
    apiPost(
      `${BASE}/api/workflows/tests/${encodeURIComponent(id)}/${encodeURIComponent(name)}/delete`,
      {},
    ),

  // Editor palette catalog — extends the legacy registry endpoint
  // with node_types + trigger_types so the FE no longer hard-codes
  // the palette list. Same URL the Drawflow editor consumes; we get
  // a superset.
  catalog: (): Promise<CatalogResponse> =>
    apiGet(`${BASE}/workflows/api/registry`),

  // Editor "Add node" picker tree. Backend owns category + label +
  // badge + drill structure so registering a new node executor /
  // channel / connector lights up the picker with zero FE edits.
  palette: (): Promise<PaletteResponse> =>
    apiGet(`${BASE}/api/workflows/palette`),

  // Picker resolver — backs `wick:"picker=slack.channels"` fields in
  // event match schemas / config forms. Module is the channel name
  // (e.g. "slack"), source is the registry key the channel's
  // LookupProvider understands. Returns `[{id, name}, ...]`.
  lookup: (
    module: string,
    source: string,
    q: string,
  ): Promise<{ id: string; name: string }[]> =>
    apiGet(
      `${BASE}/workflows/api/lookup?module=${encodeURIComponent(module)}&source=${encodeURIComponent(source)}&q=${encodeURIComponent(q)}`,
    ),

  // Data table directory — workspace-level. Used by the datatable
  // inspector to surface table slug picker + per-column autocomplete.
  dataTables: (): Promise<{ slug: string; name: string }[]> =>
    apiGet(`${BASE}/api/data-tables`),
  dataTableColumns: (
    slug: string,
  ): Promise<{ name: string; type: string }[]> =>
    apiGet(`${BASE}/api/data-tables/${encodeURIComponent(slug)}/columns`),
  projectOptions: (): Promise<{ id: string; name: string; path: string }[]> =>
    apiGet(`${BASE}/projects/options`),

  // n8n-style "Execute step" — run a single node in isolation against
  // an optional parent input + event envelope. Server runs the
  // executor against the current draft env; nothing persists to runs/.
  execNode: (
    workflowID: string,
    body: {
      node: unknown;
      input?: Record<string, unknown>;
      event?: Record<string, unknown>;
      parent_id?: string;
      // Snapshot of upstream node outputs so refs like
      // {{.Node.<label>.row}} resolve when the FE runs a single
      // node in isolation. Keyed by node id; backend builds the
      // label aliases from the loaded workflow graph.
      node_outputs?: Record<string, Record<string, unknown>>;
    },
  ): Promise<{
    ok: boolean;
    latency_ms?: number;
    output?: Record<string, unknown>;
    error?: string;
  }> =>
    apiPost(
      `${BASE}/api/workflows/exec-node/${encodeURIComponent(workflowID)}`,
      body,
    ),

  runState: (id: string, runID: string, eventsLimit?: number): Promise<any> => {
    const qs = eventsLimit === undefined ? "" : `?events_limit=${eventsLimit}`;
    return apiGet(
      `${BASE}/workflows/edit/${encodeURIComponent(id)}/runs/${encodeURIComponent(runID)}/state${qs}`,
    );
  },

  deleteRun: (id: string, runID: string): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/runs/${encodeURIComponent(id)}/${encodeURIComponent(runID)}/delete`, {}),

  // Re-run a past run: re-fires the current draft with that run's original
  // trigger event (same input). Returns {ok}; caller refreshes the runs list.
  rerunRun: (id: string, runID: string): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/runs/${encodeURIComponent(id)}/${encodeURIComponent(runID)}/rerun`, {}),

  versions: (id: string): Promise<{ versions: WorkflowVersion[] }> =>
    apiGet(`${BASE}/api/workflows/versions/${encodeURIComponent(id)}`),

  versionDetail: (id: string, versionID: number): Promise<WorkflowVersion> =>
    apiGet(
      `${BASE}/api/workflows/versions/${encodeURIComponent(id)}/${versionID}`,
    ),

  restoreVersion: (id: string, versionID: number): Promise<{ ok: boolean }> =>
    apiPost(
      `${BASE}/api/workflows/versions/${encodeURIComponent(id)}/${versionID}/restore`,
      {},
    ),

  diffVersions: (
    id: string,
    from: number,
    to: number,
  ): Promise<{ from: WorkflowVersion; to: WorkflowVersion }> =>
    apiGet(
      `${BASE}/api/workflows/versions/${encodeURIComponent(id)}/diff?from=${from}&to=${to}`,
    ),

  deleteVersion: (id: string, versionID: number): Promise<{ ok: boolean }> =>
    apiDelete(
      `${BASE}/api/workflows/versions/${encodeURIComponent(id)}/${versionID}`,
    ),

  clearVersions: (id: string): Promise<{ ok: boolean; deleted: number }> =>
    apiDelete(`${BASE}/api/workflows/versions/${encodeURIComponent(id)}`),
};
