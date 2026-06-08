// Mirror of internal/agents/workflow/types.go (Go side). Keep in sync —
// the JSON dual-mode handler serialises these structs verbatim.

export type NodeType =
  | "classify"
  | "agent"
  | "channel"
  | "connector"
  | "shell"
  | "switch"
  | "go_script"
  | "python"
  | "http"
  | "db_query"
  | "transform"
  | "branch"
  | "parallel"
  | "merge"
  | "end"
  | "datatable_get"
  | "datatable_exists"
  | "datatable_query"
  | "datatable_insert"
  | "datatable_upsert"
  | "datatable_delete"
  | "datatable_count"
  | "session_init"
  | "webhook_respond";

export type TriggerType =
  | "cron"
  | "channel"
  | "webhook"
  | "manual"
  | "schedule_at"
  | "error";

export type Edge = {
  from: string;
  to: string;
  case?: string;
  label?: string;
};

export type RetryPolicy = {
  max?: number;
  backoff?: string;
};

export type ClassifyExample = {
  text: string;
  case: string;
};

// Node body is a flat union — the executor reads only the subset relevant
// to `type`. The validator rejects nodes that set fields outside their
// type. Keeping the shape flat mirrors the Go struct and avoids a
// discriminated-union switch for every field access.
export type Node = {
  id: string;
  type: NodeType;
  label?: string;
  description?: string;
  timeout_sec?: number;
  retry?: RetryPolicy;
  on_failure?: string;
  fallback?: string;
  output_schema?: Record<string, unknown>;

  // parallel
  branches?: string[];

  // merge
  inputs?: string[];
  strategy?: string;

  // classify + agent
  provider?: string;
  preset?: string;
  prompt?: string;
  prompt_file?: string;
  session?: string;
  session_from?: string;

  // session_init
  session_id?: string;

  // classify
  output_cases?: string[];
  structured_output?: boolean;
  normalize?: boolean;
  fuzzy_match?: boolean;
  retry_on_mismatch?: number;
  confidence_threshold?: number;
  examples?: ClassifyExample[];

  // agent
  workspace?: string;
  skills?: string[];
  tools?: string[];
  max_turns?: number;

  // channel + connector
  channel?: string;
  op?: string;
  args?: Record<string, unknown>;
  arg_modes?: Record<string, string>;

  // connector
  module?: string;
  row_id?: string;

  // datatable_*
  table?: string;
  conditions?: DataTableCond[];
  condition_modes?: Record<string, string>;
  row_modes?: Record<string, string>;
  key?: Record<string, unknown>;
  row?: Record<string, unknown>;
  order_by?: DataTableOrder[];
  limit?: number;
  offset?: number;

  // shell
  command?: string[];
  env?: Record<string, string>;
  cwd?: string;
  parse_output?: string;
  // shell command timeout — string like "30s" or "1m". Distinct from
  // `timeout_sec` which the engine reads for every other node type.
  timeout?: string;

  // http
  method?: string;
  url?: string;
  headers?: Record<string, string>;
  query?: Record<string, string>;
  body?: string;
  parse_response?: string;

  // db_query
  database?: string;
  sql?: string;
  sql_args?: string[];

  // transform / go_script
  engine?: string;
  input?: string;
  expression?: string;
  code?: string;

  // branch
  expr?: string;

  // switch — first-match-wins rule list. Storage key is "cases" to
  // match the Go side (workflow.Node.Cases). The earlier `rules` name
  // was an editor-only leftover that never round-tripped.
  cases?: SwitchCase[];
  default_case?: string;

  // session_init — `preset` selects the built-in sharing mode
  // (workflow_run / workflow_global / new). `session_id` is a literal
  // or template string that wins over preset when set. `workspace`
  // overrides the per-run workspace.
  // (preset field shared with classify/agent above)

  // end
  result?: string;

  // webhook_respond
  respond_status?: number;
  respond_body?: string;
  respond_headers?: Record<string, string>;

  // editor-only: mock input JSON used when Execute step has no parent
  // output yet. Not consumed by the engine — purely a UX scratchpad.
  mock_input?: string;

  // canvas position (engine ignores; canvas persists)
  _canvas?: { x?: number; y?: number };
};

export type SwitchCase = { when: string; case: string };
// Back-compat alias — earlier components imported SwitchRule. Drop
// once everything has migrated.
export type SwitchRule = SwitchCase;

export type DataTableCond = {
  column: string;
  op: string;
  value?: unknown;
};

export type DataTableOrder = {
  column: string;
  direction?: string;
};

export type Trigger = {
  id?: string;
  type: TriggerType;
  entry_node?: string;

  // cron
  schedule?: string;
  timezone?: string;

  // channel
  channel?: string;
  event?: string;
  target?: string;
  match?: Record<string, unknown>;
  match_enabled?: boolean;
  match_modes?: Record<string, string>;
  whitelist?: unknown;
  dedup_ttl_sec?: number;
  reply_source?: boolean;

  // webhook
  path?: string;
  method?: string;
  secret_ref?: string;
  parse_body?: string;
  body_to_var?: string;
  respond_mode?: "immediately" | "last_node" | "respond_node";

  // manual
  label?: string;
  button_label?: string;
  require_role?: string;

  // schedule_at
  at?: string;
  delete_after?: boolean;
};

export type QueuePolicy = {
  max_size?: number;
  on_overflow?: "drop_oldest" | "drop_new" | "reject";
};

export type Graph = {
  entry: string;
  nodes: Node[];
  edges: Edge[];
};

export type EnvField = {
  key: string;
  label?: string;
  required?: boolean;
  secret?: boolean;
  default?: string;
};

export type DataTableBinding = {
  alias: string;
  table: string;
};

export type Workflow = {
  id: string;
  version: number;
  name: string;
  description?: string;
  enabled: boolean;
  max_duration_sec?: number;
  triggers: Trigger[];
  queue?: QueuePolicy;
  env?: EnvField[];
  data_tables?: DataTableBinding[];
  graph: Graph;
  on_error?: { node: string };
  created_by?: string;
  created_at?: string;
  // Workflow-level canvas metadata. The Go side preserves this via a
  // map[string]any so node + trigger positions survive a JSON ↔ YAML
  // round-trip even though Node itself has no _canvas field. Editor
  // saveDraft flattens per-node _canvas into _canvas.positions[id].
  _canvas?: {
    positions?: Record<string, { x?: number; y?: number }>;
    [k: string]: unknown;
  };
};

// Version history snapshot returned by the new repository layer.
export type WorkflowVersion = {
  id: number;
  workflow_id: string;
  kind: "draft" | "published";
  body: string;
  message?: string;
  created_by?: string;
  created_at: string;
};
