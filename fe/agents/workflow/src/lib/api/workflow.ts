import { apiGet, apiPost, apiDelete } from "./client";
import type { Workflow, WorkflowVersion } from "$lib/types/workflow";

const BASE = "/tools/agents";

export type WorkflowSummary = {
  id: string;
  name: string;
  enabled: boolean;
  has_draft: boolean;
  updated_at?: string;
};

export type WorkflowGetResponse = {
  workflow: Workflow;
  draft?: Workflow;
  has_draft: boolean;
};

export type ValidationIssue = {
  severity: "error" | "warning";
  node?: string;
  field?: string;
  message: string;
  hint?: string;
};

export type ValidationReport = {
  ok: boolean;
  issues: ValidationIssue[];
};

export type CatalogResponse = {
  node_types: { type: string; description: string; when_to_use?: string; schema?: Record<string, unknown>; example?: string }[];
  trigger_types: { type: string; label: string; description: string }[];
  channels: { name: string; supports_session: boolean }[];
  connectors: { module: string; name: string }[];
  providers: { name: string; is_default: boolean }[];
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
  trigger_id?: string;
  error?: string;
};

export type WorkflowsRegistry = {
  workflows: WorkflowSummary[];
};

// Routes mounted by internal/tools/agents/spa_workflows.go. JSON-only.
export const workflowAPI = {
  list: (): Promise<WorkflowsRegistry> => apiGet(`${BASE}/api/workflows/list`),

  get: (id: string): Promise<WorkflowGetResponse> =>
    apiGet(`${BASE}/api/workflows/get/${encodeURIComponent(id)}`),

  saveDraft: (id: string, body: { yaml: string } | Workflow): Promise<{ ok: boolean }> =>
    // Backend accepts both shapes: {yaml: "..."} envelope OR raw
    // Workflow JSON (see normaliseWorkflowBody on the Go side).
    apiPost(`${BASE}/api/workflows/save/${encodeURIComponent(id)}`, body),

  publish: (id: string, _message?: string): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/publish/${encodeURIComponent(id)}`, {}),

  discardDraft: (id: string): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/discard/${encodeURIComponent(id)}`, {}),

  toggle: (id: string, enabled: boolean): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/toggle/${encodeURIComponent(id)}`, { enabled }),

  runNow: (id: string): Promise<{ ok: boolean }> =>
    apiPost(`${BASE}/api/workflows/run/${encodeURIComponent(id)}`, {}),

  runs: async (id: string): Promise<{ runs: RunSummary[]; page: number; has_more: boolean }> => {
    const res = await apiGet<{ runs: any[]; page: number; has_more: boolean }>(
      `${BASE}/api/workflows/runs/${encodeURIComponent(id)}`,
    );
    // Normalise: ensure each row has both `id` and `run_id` so panels
    // that wrote `r.run_id` (legacy) still work.
    const runs = (res.runs ?? []).map((r) => ({
      ...r,
      run_id: r.run_id ?? r.id,
      id: r.id ?? r.run_id,
    })) as RunSummary[];
    return { runs, page: res.page, has_more: res.has_more };
  },

  remove: (id: string): Promise<{ ok: boolean }> =>
    // Backend currently exposes delete via the legacy form-mode endpoint
    // (no JSON variant yet). We send POST with no body — handler reads
    // the path param. Falls through to whatever the legacy templ flow
    // does (404 surface back if path wrong).
    apiPost(`${BASE}/workflows/edit/${encodeURIComponent(id)}/delete`, {}),

  // Bottom-panel content endpoints (Validation / Guard / Tests).
  validate: (id: string): Promise<ValidationReport> =>
    apiGet(`${BASE}/api/workflows/validate/${encodeURIComponent(id)}`),

  guard: (id: string): Promise<{ hits: any[]; ok: boolean }> =>
    apiGet(`${BASE}/api/workflows/guard/${encodeURIComponent(id)}`),

  tests: (id: string): Promise<{ cases: { name: string; assertions: number }[] }> =>
    apiGet(`${BASE}/api/workflows/tests/${encodeURIComponent(id)}`),

  // Editor palette catalog — extends the legacy registry endpoint
  // with node_types + trigger_types so the FE no longer hard-codes
  // the palette list. Same URL the Drawflow editor consumes; we get
  // a superset.
  catalog: (): Promise<CatalogResponse> =>
    apiGet(`${BASE}/workflows/api/registry`),

  runState: (id: string, runID: string): Promise<any> =>
    // Legacy endpoint already returns JSON unconditionally.
    apiGet(
      `${BASE}/workflows/edit/${encodeURIComponent(id)}/runs/${encodeURIComponent(runID)}/state`,
    ),

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
};
