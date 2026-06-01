import { writable, derived, get } from "svelte/store";
import type { Workflow, Node, Edge, Trigger } from "$lib/types/workflow";
import { workflowAPI } from "$lib/api/workflow";

// Selected node id for the inspector. Null when nothing focused.
// Kept for backward compat with single-select paths; the inspector
// modal only opens for one node at a time. Multi-select moves to
// `selectedNodeIDs` below.
export const selectedNodeID = writable<string | null>(null);

// Multi-selection set — populated by marquee drag, shift-click, or
// programmatic ops (select-all etc.). Single-click on a node resets
// this set to just that one id so the legacy single-select behaviour
// is preserved.
export const selectedNodeIDs = writable<Set<string>>(new Set());

// Palette panel visibility. Hidden by default to give the canvas the
// full viewport; the floating "+" button on the canvas overlay toggles
// it. Mirrors the legacy editor's "Add node" pattern.
export const paletteOpen = writable<boolean>(false);

// Node detail modal — open on double-click. Single-click only sets
// `selectedNodeID` (highlight ring). The modal shows a full n8n-style
// 3-column layout (Input | Parameters+Settings+Execute step | Output)
// so editing a node's config doesn't fight the canvas for screen
// real-estate.
export const detailNodeID = writable<string | null>(null);

// Trigger detail modal — sibling of detailNodeID, kept separate because
// triggers live on `wf.triggers[]` rather than `wf.graph.nodes[]` and
// the inspector renders type-specific forms (cron expr, channel +
// event picker, webhook path/method, manual button label, …). Mirrors
// the legacy editor_inspector.templ trigger panel — see
// internal/tools/agents/view/workflow/editor_inspector.templ.
export const detailTriggerID = writable<string | null>(null);

// Live execution feedback — per-node status overlay (✓ success, ✗
// failed, … running). Populated by the run-now flow and SSE stream
// when those land. Engine fires per-node events; reducer here keys
// them on the run's most recent state.
export const runStatusByNode = writable<Record<string, "success" | "failed" | "running">>({});

// Last run summary — drives the "Run completed in XXms" toast at the
// top of the editor.
export const lastRunSummary = writable<{ runID: string; status: string; durationMs: number } | null>(null);

// Current draft workflow document. Source-of-truth for canvas + inspector.
export const draftWorkflow = writable<Workflow | null>(null);

// Last loaded published copy — used for diff against draft + the
// "discard draft → revert" path.
export const publishedWorkflow = writable<Workflow | null>(null);

// Dirty when draft diverges from published (cheap shallow JSON compare —
// good enough for the gate label; deep diff lives in the version-history
// panel).
export const dirty = derived(
  [draftWorkflow, publishedWorkflow],
  ([$d, $p]) => {
    if (!$d || !$p) return false;
    return JSON.stringify($d) !== JSON.stringify($p);
  },
);

export const selectedNode = derived(
  [draftWorkflow, selectedNodeID],
  ([$wf, $id]) => {
    if (!$wf || !$id) return null;
    return $wf.graph?.nodes?.find((n) => n.id === $id) ?? null;
  },
);

export async function loadWorkflow(id: string) {
  const res = await workflowAPI.get(id);
  publishedWorkflow.set(hydrate(res.workflow));
  draftWorkflow.set(hydrate(res.draft ?? structuredClone(res.workflow)));
}

// hydrate normalises the workflow shape — backend may leave nullable
// fields out when empty so the canvas can rely on `.nodes` + `.edges`
// being real arrays. Without this the first render crashes before
// loadWorkflow returns.
function hydrate(wf: Workflow): Workflow {
  if (!wf.graph) wf.graph = { entry: "", nodes: [], edges: [] };
  if (!wf.graph.nodes) wf.graph.nodes = [];
  if (!wf.graph.edges) wf.graph.edges = [];
  if (!wf.triggers) wf.triggers = [];
  // Canvas positions live on the workflow-level `_canvas.positions`
  // map (one entry per node id) — that's the on-disk shape the legacy
  // editor.js + Drawflow round-trip uses. The Svelte canvas reads
  // `node._canvas.{x,y}` per node, so unpack the map here once at
  // load time. Triggers are nodes too: they appear in
  // `_canvas.positions` keyed by trigger.id, hydrate those too.
  const positions = (wf as any)._canvas?.positions as
    | Record<string, { x?: number; y?: number }>
    | undefined;
  if (positions) {
    for (const node of wf.graph.nodes) {
      const p = positions[node.id];
      if (p && !node._canvas) {
        node._canvas = { x: p.x ?? 0, y: p.y ?? 0 };
      }
    }
  }
  return wf;
}

// ensureGraph normalises a workflow before any mutation — backend may
// omit the entire graph block when nothing has been added yet, so we
// hydrate empty defaults rather than crashing on `.nodes` undefined.
function ensureGraph(wf: Workflow): Workflow {
  if (!wf.graph) wf.graph = { entry: "", nodes: [], edges: [] };
  if (!wf.graph.nodes) wf.graph.nodes = [];
  if (!wf.graph.edges) wf.graph.edges = [];
  return wf;
}

export function updateNode(id: string, patch: Partial<Node>) {
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    const idx = wf.graph.nodes.findIndex((n) => n.id === id);
    if (idx < 0) return wf;
    wf.graph.nodes[idx] = { ...wf.graph.nodes[idx], ...patch };
    return wf;
  });
}

export function addNode(node: Node) {
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    wf.graph.nodes = [...wf.graph.nodes, node];
    return wf;
  });
}

export function updateTrigger(id: string, patch: Partial<Trigger>) {
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    const idx = (wf.triggers ?? []).findIndex((t) => t.id === id);
    if (idx < 0) return wf;
    wf.triggers[idx] = { ...wf.triggers[idx], ...patch };
    return wf;
  });
}

export function removeTrigger(id: string) {
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    wf.triggers = (wf.triggers ?? []).filter((t) => t.id !== id);
    return wf;
  });
}

export function removeNode(id: string) {
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    wf.graph.nodes = wf.graph.nodes.filter((n) => n.id !== id);
    wf.graph.edges = wf.graph.edges.filter(
      (e) => e.from !== id && e.to !== id,
    );
    return wf;
  });
}

export function connect(edge: Edge) {
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    wf.graph.edges = [...wf.graph.edges, edge];
    return wf;
  });
}

export function disconnect(from: string, to: string, caseKey?: string) {
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    wf.graph.edges = wf.graph.edges.filter(
      (e) =>
        !(
          e.from === from &&
          e.to === to &&
          (caseKey === undefined || e.case === caseKey)
        ),
    );
    return wf;
  });
}

export async function saveDraft() {
  const wf = get(draftWorkflow);
  if (!wf) return;
  await workflowAPI.saveDraft(wf.id, wf);
}

export async function publish(message?: string) {
  const wf = get(draftWorkflow);
  if (!wf) return;
  await workflowAPI.publish(wf.id, message);
  publishedWorkflow.set(structuredClone(wf));
}
