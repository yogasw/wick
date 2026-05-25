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
  | "session_init";

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

  // shell
  command?: string[];
  env?: Record<string, string>;
  cwd?: string;
  parse_output?: string;

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

  // switch — rule list (canonical shape lives in editor)
  rules?: SwitchRule[];

  // canvas position (engine ignores; canvas persists)
  _canvas?: { x?: number; y?: number };
};

export type SwitchRule = { when: string; case: string };

export type Trigger = {
  id?: string;
  type: TriggerType;
  entry_node?: string;
  schedule?: string;
  // cron
  expr?: string;
  // channel
  channel?: string;
  match?: Record<string, unknown>;
  event?: string;
  // webhook
  path?: string;
  method?: string;
  // schedule_at
  at?: string;
  // manual
  label?: string;
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
};

// Version history snapshot returned by the new repository layer.
export type WorkflowVersion = {
  id: number;
  workflow_id: string;
  kind: "draft" | "published";
  yaml: string;
  message?: string;
  created_by?: string;
  created_at: string;
};
