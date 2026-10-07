// Output ports — one labelled port per verdict a node can route to,
// n8n style. Nodes with a single unconditional output return [] and keep
// the plain bottom-centre port; case-routed nodes (the Go side's
// NodeType.IsBranchSource) get one port per case, stacked on the card's
// right edge so long names stay readable.
//
// The engine follows the outgoing edges whose `case` equals the node's
// verdict and falls back to the `default` edge, so every port name here
// is an edge case.

import type { Edge, Node, Workflow } from "$lib/types/workflow";

// Verdicts these nodes always emit; they have no default catch-all.
const FIXED_PORTS: Record<string, string[]> = {
  datatable_get: ["found", "not_found"],
  datatable_exists: ["true", "false"],
};

const CASE_ROUTED = new Set(["branch", "classify", "switch", "datatable_get", "datatable_exists"]);

export function isCaseRouted(type: string): boolean {
  return CASE_ROUTED.has(type);
}

// Node types whose output names the user defines (output_cases).
export function hasEditablePorts(type: string): boolean {
  return type === "branch" || type === "classify";
}

export function fallbackPort(node: Node): string {
  return node.type === "switch" && node.default_case ? node.default_case : "default";
}

// outputPorts lists the node's output names in display order: the
// declared ones first, then any case an existing edge already uses (so a
// legacy edge never loses its port), then the fallback last.
export function outputPorts(node: Node | undefined, edges: Edge[]): string[] {
  if (!node || !isCaseRouted(node.type)) return [];
  const out: string[] = [];
  const add = (c: string | undefined) => {
    const v = (c ?? "").trim();
    if (v && !out.includes(v)) out.push(v);
  };
  const fixed = FIXED_PORTS[node.type];
  if (fixed) fixed.forEach(add);
  else if (node.type === "switch") (node.cases ?? []).forEach((r) => add(r.case));
  else (node.output_cases ?? []).forEach(add);

  const fallback = fixed ? "" : fallbackPort(node);
  for (const e of edges) {
    if (e.from === node.id && e.case !== fallback) add(e.case);
  }
  if (fallback) {
    const i = out.indexOf(fallback);
    if (i >= 0) out.splice(i, 1);
    out.push(fallback);
  }
  return out;
}

// A port as painted on the card. `key` is what an edge carries: the case
// for a case port, "" for the plain success output, ERROR_KEY for the
// on_failure=fallback path (stored as node.fallback, not as an edge).
export type OutPort = { key: string; label: string; kind: "case" | "success" | "error" };
export const ERROR_KEY = "\u0000error";

export function hasErrorOutput(node: Node | undefined): boolean {
  return node?.on_failure === "fallback";
}

// nodeOutPorts is every labelled output of the node: its cases (or a
// plain "success" when it has none) plus "error" when failures route to
// a fallback node. [] = one unlabelled output, the bottom-centre port.
export function nodeOutPorts(node: Node | undefined, edges: Edge[]): OutPort[] {
  const cases: OutPort[] = outputPorts(node, edges).map((c) => ({ key: c, label: c, kind: "case" }));
  if (!hasErrorOutput(node)) return cases;
  const main: OutPort[] = cases.length ? cases : [{ key: "", label: "success", kind: "success" }];
  return [...main, { key: ERROR_KEY, label: "error", kind: "error" }];
}

// An incoming connection. Shown as its own port once a node has more
// than one, so a split (e.g. a success path and a failure path landing
// on the same node) stays readable.
export type InPort = {
  key: string;
  from: string;
  label: string;
  detail?: string; // case or "error"
  trigger?: boolean;
  connected: boolean;
  desc?: string; // the source's own description, for the hover tooltip
};

export function edgeInKey(from: string, caseLabel?: string): string {
  return `e:${from}:${caseLabel ?? ""}`;
}
export const triggerInKey = (id: string) => `t:${id}`;
export const fallbackInKey = (from: string) => `f:${from}`;

// incomingPorts lists every source feeding the node, ordered left to
// right by the source's canvas x so the stacked rows cross as little as
// possible. Merge nodes also list awaited inputs nothing is wired to yet.
export function incomingPorts(node: Node | undefined, wf: Workflow | null | undefined): InPort[] {
  if (!node || !wf) return [];
  const nodes = wf.graph?.nodes ?? [];
  const positions = ((wf as any)._canvas?.positions ?? {}) as Record<string, { x?: number }>;
  const nodeX = (id: string) => nodes.find((n) => n.id === id)?._canvas?.x ?? 0;
  const labelOf = (id: string) => nodes.find((n) => n.id === id)?.label || id;
  const descOf = (id: string) => nodes.find((n) => n.id === id)?.description || undefined;
  const out: (InPort & { x: number })[] = [];
  for (const t of wf.triggers ?? []) {
    if (t.id && t.entry_node === node.id) {
      out.push({ key: triggerInKey(t.id), from: t.id, label: t.id, detail: t.type, trigger: true, connected: true, desc: t.description || undefined, x: positions[t.id]?.x ?? 60 });
    }
  }
  for (const e of wf.graph?.edges ?? []) {
    if (e.to !== node.id) continue;
    const key = edgeInKey(e.from, e.case);
    if (!out.some((p) => p.key === key)) {
      out.push({ key, from: e.from, label: labelOf(e.from), detail: e.case, connected: true, desc: descOf(e.from), x: nodeX(e.from) });
    }
  }
  for (const n of nodes) {
    if (n.id !== node.id && hasErrorOutput(n) && n.fallback === node.id) {
      out.push({ key: fallbackInKey(n.id), from: n.id, label: labelOf(n.id), detail: "error", connected: true, desc: n.description || undefined, x: nodeX(n.id) });
    }
  }
  if (node.type === "merge") {
    for (const id of node.inputs ?? []) {
      if (!out.some((p) => p.from === id && !p.trigger)) {
        out.push({ key: edgeInKey(id), from: id, label: labelOf(id), connected: false, desc: descOf(id), x: nodeX(id) });
      }
    }
  }
  return out.sort((a, b) => a.x - b.x).map(({ x: _x, ...p }) => p);
}

// inputPorts is what the card paints: one row per source once there is
// more than one, [] otherwise (the single top-centre input).
export function inputPorts(node: Node | undefined, wf: Workflow | null | undefined): InPort[] {
  const all = incomingPorts(node, wf);
  return all.length > 1 ? all : [];
}

// Tone per port name, light + dark. Only shades present in the shared
// tailwind palette — anything else is purged and renders unstyled.
export function portTone(name: string): string {
  if (name === ERROR_KEY) return "bg-rose-100 text-rose-700 dark:bg-rose-500/20 dark:text-rose-300";
  if (name === "default") return "bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200";
  if (name === "false" || name === "not_found") return "bg-rose-100 text-rose-700 dark:bg-rose-500/20 dark:text-rose-300";
  return "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300";
}

// Geometry shared by BaseNode (which paints the ports) and Canvas (which
// anchors edges on them). The flow runs top → bottom, so several ports
// on one side split the card's width into equal columns: outputs along
// the bottom edge, inputs along the top edge.
export function portCenterX(cardLeft: number, cardWidth: number, count: number, i: number): number {
  return cardLeft + ((i + 0.5) * cardWidth) / count;
}

// Cards are 220px wide, but a node with many outputs (or inputs) widens
// so each port column keeps room for a flat, readable label (~12
// characters). Past WIDE_PORTS_MAX the card would get unwieldy, so that
// side stops widening it: outputs stand upright instead, inputs truncate
// (full text in the hover tooltip either way).
export const CARD_W = 220;
export const PORT_COL_MIN = 90;
export const WIDE_PORTS_MAX = 6;
export function flatPortLabels(outCount: number): boolean {
  return outCount <= WIDE_PORTS_MAX;
}
export function cardWidth(outCount: number, inCount = 0): number {
  const cols = Math.max(flatPortLabels(outCount) ? outCount : 0, inCount <= WIDE_PORTS_MAX ? inCount : 0);
  return Math.max(CARD_W, cols * PORT_COL_MIN);
}
