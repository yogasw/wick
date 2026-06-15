export interface ConnectorDef {
  key: string;
  name: string;
  category: string;
  icon: string;
  custom?: boolean;
  disabled?: boolean;
}

export interface ConnectorRow {
  id: string;
  label: string;
  disabled: boolean;
  status: string;
  rate_limit_rpm: number;
  tags: string[] | null;
}

export interface ConnectorList {
  key: string;
  name: string;
  description: string;
  icon: string;
  fixed: boolean;
  op_count: number;
  custom: boolean;
  rows: ConnectorRow[] | null;
}

export interface ConfigField {
  key: string;
  type: string;
  value: string;
  options: string;
  required: boolean;
  is_secret: boolean;
  has_value: boolean;
  description: string;
  visible_when: string;
  col_options?: Record<string, string>;
  env_override: string;
}

export interface ConnectorOp {
  key: string;
  name: string;
  description: string;
  destructive: boolean;
  enabled: boolean;
  system_disabled: boolean;
  system_disabled_reason: string;
}

export interface ConnectorDetail {
  key: string;
  name: string;
  icon: string;
  id: string;
  label: string;
  disabled: boolean;
  rate_limit_rpm: number;
  has_health_check: boolean;
  can_configure: boolean;
  fields: ConfigField[] | null;
  operations: ConnectorOp[] | null;
}

export interface HealthCheckResult {
  ok: boolean;
  error?: string;
  newly_locked?: string[] | null;
  newly_cleared?: string[] | null;
  ops?: Record<string, { enabled: boolean; system_disabled: boolean; reason: string }>;
}
