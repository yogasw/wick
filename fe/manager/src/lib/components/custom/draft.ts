import type { Draft, DraftField, DraftOp, DraftCategory } from "$lib/types.js";

export const WIDGETS = [
  "text",
  "textarea",
  "dropdown",
  "number",
  "checkbox",
  "bool",
  "secret",
  "email",
  "url",
  "date",
  "datetime",
];

export const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

export function newField(): DraftField {
  return { key: "", widget: "text", options: "", secret: false, required: false, default: "", desc: "" };
}

export function newOp(): DraftOp {
  return {
    key: "",
    name: "",
    description: "",
    destructive: false,
    inputs: [],
    request: { method: "GET", url_template: "{{.cfg.base_url}}/", headers: {}, body_template: "", content_type: "" },
  };
}

export function newCategory(): DraftCategory {
  return { title: "", description: "", ops: [] };
}

/* allOps flattens a draft's sections into a single op list — for the
   health-probe picker and any flat count. */
export function allOps(d: Draft): DraftOp[] {
  return d.ops.flatMap((c) => c.ops);
}

function normalizeField(f: Partial<DraftField>): DraftField {
  return {
    key: f.key ?? "",
    widget: f.widget ?? "text",
    options: f.options ?? "",
    secret: !!f.secret,
    required: !!f.required,
    default: f.default ?? "",
    desc: f.desc ?? "",
  };
}

function normalizeOp(op: Partial<DraftOp>): DraftOp {
  const out: DraftOp = {
    key: op.key ?? "",
    name: op.name ?? "",
    description: op.description ?? "",
    destructive: !!op.destructive,
    inputs: (op.inputs ?? []).map(normalizeField),
    mcp_source: op.mcp_source,
  };
  if (!op.mcp_source && !op.request) {
    out.request = { method: "GET", url_template: "", headers: {}, body_template: "", content_type: "" };
  } else if (op.request) {
    out.request = { ...op.request, headers: op.request.headers ?? {} };
  }
  return out;
}

function normalizeCategory(c: Partial<DraftCategory>): DraftCategory {
  return {
    title: c.title ?? "",
    description: c.description ?? "",
    ops: (c.ops ?? []).map(normalizeOp),
  };
}

/* normalize coerces a partial parse/edit payload into a full Draft so the
   form binds against stable fields, mirroring the legacy normalize(). */
export function normalize(d: Partial<Draft> | null | undefined): Draft {
  const src = d ?? {};
  return {
    key: src.key ?? "",
    name: src.name ?? "",
    description: src.description ?? "",
    icon: src.icon ?? "🔌",
    source: src.source ?? "manual",
    category: src.category ?? "",
    single: !!src.single,
    allow_session_config: !!src.allow_session_config,
    health_op: src.health_op ?? "",
    health_expect: src.health_expect ?? "",
    configs: (src.configs ?? []).map(normalizeField),
    /* Always have at least one section so the builder can add ops into it.
       A fresh manual draft starts with a single untitled section. */
    ops: (src.ops?.length ? src.ops : [{}]).map(normalizeCategory),
  };
}

/* serialize produces the exact JSON the save endpoint consumes — what the
   live preview renders, what the toolbar Save posts. */
export function serialize(d: Draft): Draft {
  return {
    key: d.key,
    name: d.name,
    description: d.description,
    icon: d.icon || "🔌",
    source: d.source,
    category: d.category,
    single: d.single,
    allow_session_config: d.allow_session_config,
    health_op: d.health_op,
    health_expect: d.health_expect,
    configs: d.configs,
    ops: d.ops,
  };
}
