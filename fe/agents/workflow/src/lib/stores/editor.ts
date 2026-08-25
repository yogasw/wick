import { writable, derived, get } from "svelte/store";
import type { Workflow, Node, Edge, Trigger } from "$lib/types/workflow";
import { workflowAPI, type ValidationReport, type ValidationIssue, type WorkflowState, type PaletteDrag } from "$lib/api/workflow";
import { APIError } from "@wick-fe/common-api";
import { toastError, toastOk } from "@wick-fe/common-stores";
import { renameNodeRefs, type GraphNode } from "$lib/components/workflow/renameNodeRefs";

// Decorate raw {Path, Message} issues with severity + node so consumers
// (toolbar chip, ValidationTab) don't repeat the extraction logic.
function decorateReport(r: ValidationReport | undefined | null): ValidationReport | null {
  if (!r) return null;
  const tag = (i: ValidationIssue, sev: "error" | "warning"): ValidationIssue => {
    const m = i.Path?.match(/graph\.nodes\[([^\]]+)\]/);
    return { ...i, severity: sev, node: m ? m[1] : undefined, field: i.Path };
  };
  return {
    ok: r.ok,
    errors: (r.errors ?? []).map((i) => tag(i, "error")),
    warnings: (r.warnings ?? []).map((i) => tag(i, "warning")),
    by_node: r.by_node,
    global: r.global,
  };
}

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

// Tap-to-add bridge. HTML5 drag-and-drop (how the palette drops nodes
// onto the canvas) is dead on touch screens, so on a tap the Palette
// pushes the item's drag payload here and the Canvas — which owns the
// pan / zoom transform needed to compute a sensible drop point — picks
// it up, places the node at the current viewport centre, and resets
// this back to null. Works on mouse too (click = add at centre).
export const paletteAddRequest = writable<PaletteDrag | null>(null);

// Search overlay open state — toggled from the top-right magnifier
// button + Ctrl/Cmd+K. Centralised so EditorShell can mount the
// overlay component and Canvas can flip it open without prop drilling.
export const searchOpen = writable<boolean>(false);

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

// Execute-step results retained across modal close/reopen. Keyed by
// node id so the inspector's INPUT pane on a child node can read its
// parent's last output, and OUTPUT pane keeps showing the most recent
// run after the user closes + reopens. Cleared by loadWorkflow.
export type StepResult = {
  ok: boolean;
  output?: Record<string, unknown>;
  input?: Record<string, unknown>;
  parent_id?: string;
  error?: string;
  latency_ms?: number;
  at: number;
};
export const stepResultsByNode = writable<Record<string, StepResult>>({});

// Per-trigger run status — set when the user fires a trigger via the
// Execute button + cleared on next workflow_started for the same id.
// Backend RunEvent doesn't carry a trigger id, so we rely on the FE
// remembering which trigger was just clicked (cron / external triggers
// don't get a badge — only manual runs).
export const triggerRunStatus = writable<Record<string, "success" | "failed" | "running">>({});
export const lastFiredTriggerID = writable<string | null>(null);

// Pinned trigger for the Execute button. Lifted out of Canvas so the
// Executions panel can pin a trigger via Replay (per-workflow scope,
// persisted to localStorage). Stays in editor.ts because both Canvas
// and EditorShell mutate it.
export const pinnedTriggerID = writable<string | null>(null);
function pinKey(workflowID: string) {
  return `wick:wfv2:pinned-trigger:${workflowID}`;
}
export function loadPinnedTrigger(workflowID: string): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(pinKey(workflowID));
  } catch {
    return null;
  }
}
export function savePinnedTrigger(workflowID: string, triggerID: string | null) {
  pinnedTriggerID.set(triggerID);
  if (typeof window === "undefined") return;
  try {
    if (triggerID) window.localStorage.setItem(pinKey(workflowID), triggerID);
    else window.localStorage.removeItem(pinKey(workflowID));
  } catch {
    /* storage full / denied — in-memory pin still works for this session */
  }
}

// Last-fired event payload per trigger id. Populated by Replay-to-editor
// (which carries the picked run's event.payload) so the TriggerDetailModal
// OUTPUT pane can show what the trigger received — the same "surfaces once
// a run lands" affordance the node inspector gets from stepResultsByNode.
// SSE workflow_started doesn't carry the payload, so replay is the feed.
export const triggerEventByID = writable<Record<string, unknown>>({});
export function setTriggerEvent(triggerID: string, payload: unknown) {
  triggerEventByID.update((m) => ({ ...m, [triggerID]: payload }));
}
// unpinTriggerEvent clears a replayed payload — n8n-style "unpin data".
// After this the trigger falls back to the synthetic Execute payload and
// the OUTPUT pane goes back to "No event data".
export function unpinTriggerEvent(triggerID: string) {
  triggerEventByID.update((m) => {
    if (!(triggerID in m)) return m;
    const { [triggerID]: _, ...rest } = m;
    return rest;
  });
}

// HydrateSummary reports what hydrateFromRun actually populated, so the
// caller can give honest feedback instead of always claiming success.
// "replay sukses tapi kosong" happens for runs that genuinely carry no
// data — failed at the first node (no outputs) or a legacy manual Execute
// whose payload is just the {source:spa,trigger_id} placeholder.
export type HydrateSummary = {
  nodeCount: number; // nodes that got a ✓/✗ status overlay
  outputCount: number; // nodes with a stored output
  triggerPinned: boolean; // a meaningful event payload was pinned
  syntheticPayload: boolean; // payload was the {source:spa} placeholder
};

// isSyntheticPayload detects the placeholder a plain manual Execute writes
// ({source:"spa", trigger_id:"..."}) — there's no real data to pin.
function isSyntheticPayload(p: unknown): boolean {
  if (!p || typeof p !== "object") return false;
  const keys = Object.keys(p as Record<string, unknown>);
  return keys.length === 2 && keys.includes("source") && keys.includes("trigger_id")
    && (p as Record<string, unknown>).source === "spa";
}

// hydrateFromRun replays a past run's full state onto the canvas — the
// n8n "import execution" affordance. It fills three stores so opening a
// run looks exactly like having just executed it:
//   - runStatusByNode: ✓/✗/skip from completed/failed/skipped arrays
//   - stepResultsByNode: each node's output (+ the failed node's error)
//     from the Outputs map, so the node inspector shows real I/O
//   - triggerEventByID: the trigger event payload, so the entry node's
//     {{.Event.Payload.x}} resolves and Execute can re-fire with it
// runDetail is the flattened run-state object the ExecutionsPanel builds
// ({...state, events}); fields mirror workflow.RunState (Go side).
// Returns a HydrateSummary so the caller can warn on empty runs.
export function hydrateFromRun(runDetail: any | null | undefined): HydrateSummary {
  const empty: HydrateSummary = { nodeCount: 0, outputCount: 0, triggerPinned: false, syntheticPayload: false };
  if (!runDetail) return empty;
  const at = Date.now();

  const status: Record<string, "success" | "failed" | "running"> = {};
  for (const id of (runDetail.completed ?? []) as string[]) status[id] = "success";
  for (const id of (runDetail.failed ?? []) as string[]) status[id] = "failed";
  // skipped nodes carry no status colour (they didn't run) — left out on
  // purpose so the overlay matches what actually executed.
  runStatusByNode.set(status);

  const outputs = (runDetail.outputs ?? {}) as Record<string, unknown>;
  const failedSet = new Set<string>((runDetail.failed ?? []) as string[]);
  const errMsg: string | undefined =
    typeof runDetail.error?.message === "string" ? runDetail.error.message : undefined;
  const errNode: string | undefined =
    typeof runDetail.error?.node === "string" ? runDetail.error.node : undefined;

  const steps: Record<string, StepResult> = {};
  for (const [nodeID, out] of Object.entries(outputs)) {
    steps[nodeID] = {
      ok: !failedSet.has(nodeID),
      output: (out ?? undefined) as Record<string, unknown> | undefined,
      at,
    };
  }
  // The failed node usually has no Outputs entry (it errored before
  // producing one) — seed it so the inspector shows the failure + error.
  if (errNode && !steps[errNode]) {
    steps[errNode] = { ok: false, error: errMsg, at };
  } else if (errNode && steps[errNode]) {
    steps[errNode] = { ...steps[errNode], ok: false, error: errMsg };
  }
  stepResultsByNode.set(steps);

  // Trigger event — keyed by the trigger that fired. Prefer the explicit
  // event.trigger_id, but older / re-run / webhook-test runs sometimes
  // carry a real payload with NO trigger_id (e.g. event.trigger_id=null).
  // In that case fall back to the trigger whose entry_node matches the
  // run's entry, then to the sole trigger — otherwise a run with good data
  // would import with an empty trigger OUTPUT ("event hilang pas replay").
  const ev = runDetail.event;
  const triggers = get(draftWorkflow)?.triggers ?? [];
  let triggerID: string | undefined =
    (typeof ev?.trigger_id === "string" && ev.trigger_id) ||
    (typeof ev?.payload?.trigger_id === "string" && ev.payload.trigger_id) ||
    (typeof runDetail.trigger_id === "string" && runDetail.trigger_id) ||
    undefined;
  if (!triggerID && ev?.payload != null) {
    const entry = typeof runDetail.entry === "string" ? runDetail.entry : "";
    const byEntry = entry ? triggers.find((t) => t.entry_node === entry) : undefined;
    triggerID = byEntry?.id ?? (triggers.length === 1 ? triggers[0]?.id : undefined);
  }
  const synthetic = isSyntheticPayload(ev?.payload);
  let triggerPinned = false;
  if (triggerID && ev?.payload != null) {
    // Pin even the synthetic payload (so Execute routes the trigger), but
    // report it as not-meaningfully-pinned so the caller can warn.
    setTriggerEvent(triggerID, ev.payload);
    triggerPinned = !synthetic;
  }
  // Trigger status overlay — a run that produced any node activity means
  // the trigger fired. Mark it failed only if the whole run failed AND no
  // node ran (trigger itself was the failure point); otherwise success.
  // This is what gives the trigger card its ✓/✗ after "import execution".
  if (triggerID) {
    const anyNodeRan = Object.keys(status).length > 0;
    const runFailed = runDetail.status === "failed";
    triggerRunStatus.update((m) => ({
      ...m,
      [triggerID]: anyNodeRan || !runFailed ? "success" : "failed",
    }));
  }

  return {
    nodeCount: Object.keys(status).length,
    outputCount: Object.keys(outputs).length,
    triggerPinned,
    syntheticPayload: synthetic,
  };
}

// Last run summary — drives the "Run completed in XXms" toast at the
// top of the editor.
export const lastRunSummary = writable<{ runID: string; status: string; durationMs: number } | null>(null);

// Save status state machine — mirrors v1's `#wf-save-status` text:
//   idle     — nothing pending, no save in flight
//   pending  — local edit detected, debounce timer running
//   saving   — POST /save in flight
//   saved    — last save completed (validation reported separately)
//   failed   — last save errored (network / server 5xx)
//
// Validation outcome lives on its own (validationReport + the chip in
// the toolbar) so the save-status pill stays focused on "did the
// bytes hit disk" — same split as v1.
export type SaveStatus = "idle" | "pending" | "saving" | "saved" | "failed";

export const saveStatus = writable<SaveStatus>("idle");

// Unix-ms timestamp of the last successful save. Toolbar derives a
// "Saved Xs ago" suffix off this via a 1 s interval $effect.
export const lastSavedAt = writable<number | null>(null);

// Latest validation report — refreshed after every save, drives the
// red error chip in the toolbar + the row list in the Validation tab.
// `null` means "not run yet"; treat as clean for gate purposes.
export const validationReport = writable<ValidationReport | null>(null);

// Quick derived: error count from the latest validation report. Used
// by the toolbar chip + by the Publish gate.
export const validationErrorCount = derived(validationReport, ($r) => {
  return $r?.errors?.length ?? 0;
});

export const validationWarningCount = derived(validationReport, ($r) => {
  return $r?.warnings?.length ?? 0;
});

// Current draft workflow document. Source-of-truth for canvas + inspector.
// Label format gate — mirrors parse.LabelRe on the Go side (see
// internal/agents/workflow/parse/parse.go). Lowercase letter or `_`
// to start, lowercase letter / digit / `_` for the rest. Surfaced in
// inspectors so the user sees the rule before save validation rejects.
export const LABEL_RE = /^[a-z_][a-z0-9_]*$/;
export const LABEL_FORMAT_HINT = "lowercase letters, digits and _ (must start with a letter or _)";
export function isValidLabel(s: string): boolean {
  return LABEL_RE.test(s);
}

export const draftWorkflow = writable<Workflow | null>(null);

// Last loaded published copy — used for diff against draft + the
// "discard draft → revert" path.
export const publishedWorkflow = writable<Workflow | null>(null);

// Governance / approval snapshot — drives the "approved vN" badge
// in the toolbar. Null when state has never been written for this
// workflow (fresh draft); the toolbar treats null as "not approved".
export const workflowState = writable<WorkflowState | null>(null);

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

// Activation gate — the runtime only schedules the *published* copy, so
// flipping `enabled` true on a workflow with no published nodes is a
// no-op that confuses operators. We treat the published copy as "real"
// once it has at least one node beyond the empty bootstrap shell.
export const canActivate = derived(publishedWorkflow, ($p) => {
  return !!($p?.graph?.nodes && $p.graph.nodes.length > 0);
});

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
  workflowState.set(res.state ?? null);
  // Reset transient state so the toolbar + toasts don't surface stale
  // status from a previously-loaded workflow. Also clear per-node run
  // overlays — otherwise a node carried "failed" from the previous
  // session, the red ring + status dot still paints on the new page.
  saveStatus.set("idle");
  lastSavedAt.set(null);
  validationReport.set(null);
  runStatusByNode.set({});
  stepResultsByNode.set({});
  // First subscriber call fires immediately with the just-set value;
  // skip that and only react to genuine post-load edits.
  autosaveArmed = false;
  // Validate on load so canvas badges appear immediately without
  // requiring the user to save first.
  workflowAPI.validate(id).then((r) => validationReport.set(decorateReport(r))).catch(() => {});
}

// Auto-save plumbing — mirrors v1 editor.js's 800 ms post-edit
// debounce. Any draftWorkflow mutation after the initial load arms
// the timer; the next save fires once edits go quiet, transitioning
// saveStatus through "pending" → "saving" → terminal.
let autosaveTimer: ReturnType<typeof setTimeout> | null = null;
let autosaveArmed = false;
const AUTOSAVE_MS = 2000;
// Set before a draftWorkflow.update that should NOT arm the autosave
// — used by the lock toggle which persists via its own endpoint, so
// the regular save path doesn't reject "you can't edit a locked
// workflow" against a workflow that's only being unlocked.
let skipNextAutosave = false;

draftWorkflow.subscribe((wf) => {
  if (!wf) return;
  if (!autosaveArmed) {
    // First update after a load — the call that delivers the initial
    // value. Real edits land later.
    autosaveArmed = true;
    return;
  }
  if (skipNextAutosave) {
    skipNextAutosave = false;
    return;
  }
  saveStatus.set("pending");
  if (autosaveTimer) clearTimeout(autosaveTimer);
  autosaveTimer = setTimeout(() => {
    autosaveTimer = null;
    // Quiet autosave — toolbar status text + validation chip carry
    // the feedback; we don't want a toast every 800 ms of editing.
    saveDraft({ silent: true }).catch((e) => console.warn("auto-save failed:", e));
  }, AUTOSAVE_MS);
});

// setLockedField updates the local store's _canvas.locked without
// arming autosave. Canvas.toggleLock calls workflowAPI.setLock first
// (server is source of truth), then mirrors here so the rest of the
// UI reacts immediately.
export function setLockedField(locked: boolean) {
  skipNextAutosave = true;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    const canvas = ((wf as any)._canvas ?? {}) as Record<string, unknown>;
    canvas.locked = locked;
    (wf as any)._canvas = canvas;
    return wf;
  });
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

// Lock gate — every mutator below short-circuits when the workflow's
// _canvas.locked flag is true. Only one path bypasses: the Canvas
// component's toggleLock, which writes the flag directly via
// draftWorkflow.update so it can clear the lock itself. Centralising
// the gate here means a future MCP / API client can also call these
// helpers without having to re-implement the check.
function isWorkflowLocked(): boolean {
  const wf = get(draftWorkflow);
  return !!((wf as any)?._canvas?.locked);
}
function lockGuard(label: string): boolean {
  if (!isWorkflowLocked()) return false;
  toastError(
    "Workflow is locked",
    `Unlock the canvas before ${label}.`,
  );
  return true;
}

export function updateNode(id: string, patch: Partial<Node>) {
  if (lockGuard("editing nodes")) return;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    const idx = wf.graph.nodes.findIndex((n) => n.id === id);
    if (idx < 0) return wf;
    wf.graph.nodes[idx] = { ...wf.graph.nodes[idx], ...patch };
    return wf;
  });
}

// renameNodeLabel commits a label change AND cascades every
// {{.Node.<oldLabel>.…}} reference across the workflow to the new label,
// so renaming a node never orphans the templates that read its output
// (n8n-style rename cascade). Call this on label-commit (blur), not on
// every keystroke. Returns the number of OTHER nodes whose refs changed
// so the UI can surface "updated N references".
export function renameNodeLabel(id: string, newLabel: string): number {
  if (lockGuard("renaming nodes")) return 0;
  let changed = 0;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    const node = wf.graph.nodes.find((n) => n.id === id);
    if (!node) return wf;
    const oldLabel = node.label ?? "";
    if (!oldLabel || oldLabel === newLabel) {
      // No cascade needed — just set the label.
      const idx = wf.graph.nodes.findIndex((n) => n.id === id);
      if (idx >= 0) wf.graph.nodes[idx] = { ...wf.graph.nodes[idx], label: newLabel };
      return wf;
    }
    const before = wf.graph.nodes.map((n) => JSON.stringify(n));
    wf.graph.nodes = renameNodeRefs(
      wf.graph.nodes as unknown as GraphNode[],
      id,
      oldLabel,
      newLabel,
    ) as unknown as typeof wf.graph.nodes;
    // Count OTHER nodes whose serialization changed (the renamed node
    // always changes — exclude it from the "references updated" count).
    wf.graph.nodes.forEach((n, i) => {
      if (n.id !== id && JSON.stringify(n) !== before[i]) changed++;
    });
    return wf;
  });
  return changed;
}

// Generate a label like `<type>_<N>` that isn't already used by any
// existing node label or id within the workflow. Mirrors the
// "duplicate label" validator on the Go side so brand-new nodes
// never fail validation just for existing.
function nextNodeLabel(wf: Workflow, type: string): string {
  const taken = new Set<string>();
  for (const n of wf.graph?.nodes ?? []) {
    if (n.label) taken.add(n.label);
    if (n.id) taken.add(n.id);
  }
  let i = 1;
  while (taken.has(`${type}_${i}`)) i++;
  return `${type}_${i}`;
}

export function addNode(node: Node) {
  if (lockGuard("adding nodes")) return;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    // Auto-fill label (and id when missing) with the next free
    // `<type>_<N>` slot so duplicates from quick drops don't trip
    // the validator. Operator can rename via the inspector after.
    // For channel + connector drops, the user has already picked a
    // specific backend (slack / github / …) — labelling them all
    // `channel_1` / `connector_1` loses that signal. Key the slot on
    // the channel name / module instead, so a Slack drop reads `slack_1`
    // and a GitHub drop reads `github_1`.
    const filled = { ...node };
    // Channel/connector drops carry channel/module + op. Combine into
    // a label key so dropping two different Slack ops yields
    // `slack_send_1` and `slack_open_1` rather than fighting over
    // `slack_1`. Falls back to the bare channel/module when op is
    // absent (manually-created node without a drill drop).
    const backend =
      (node.type === "channel" && (node as any).channel) ||
      (node.type === "connector" && (node as any).module) ||
      "";
    const opPart = (node as any).op ? `_${(node as any).op}` : "";
    const labelKey = backend ? `${backend}${opPart}` : node.type;
    if (!filled.label) {
      filled.label = nextNodeLabel(wf, labelKey);
    }
    if (!filled.id) {
      filled.id = filled.label;
    }
    wf.graph.nodes = [...wf.graph.nodes, filled];
    return wf;
  });
}

export function updateTrigger(id: string, patch: Partial<Trigger>) {
  if (lockGuard("editing triggers")) return;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    const idx = (wf.triggers ?? []).findIndex((t) => t.id === id);
    if (idx < 0) return wf;
    wf.triggers[idx] = { ...wf.triggers[idx], ...patch };
    return wf;
  });
}

export function removeTrigger(id: string) {
  if (lockGuard("deleting triggers")) return;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    wf.triggers = (wf.triggers ?? []).filter((t) => t.id !== id);
    // Triggers store their canvas position on workflow._canvas.positions
    // (the Node struct on the Go side has no _canvas field, so positions
    // for both nodes and triggers share that map). Drop the stale entry
    // so a re-added trigger with the same id doesn't snap to the ghost.
    const canvas = ((wf as any)._canvas ?? {}) as any;
    if (canvas.positions && id in canvas.positions) {
      delete canvas.positions[id];
    }
    return wf;
  });
}

export function removeNode(id: string) {
  if (lockGuard("deleting nodes")) return;
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
  if (lockGuard("adding edges")) return;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    wf.graph.edges = [...wf.graph.edges, edge];
    return wf;
  });
}

export function disconnect(from: string, to: string, caseKey?: string) {
  if (lockGuard("removing edges")) return;
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

// applyEdgeCase returns a copy of `edges` with the case of the edge
// matching (from, to, prevCase) set to nextCase. Empty nextCase removes
// the case field, turning it back into an unconditional edge. Pure —
// shared by setEdgeCase and unit-tested directly.
export function applyEdgeCase(
  edges: Edge[],
  from: string,
  to: string,
  prevCase: string | undefined,
  nextCase: string,
): Edge[] {
  return edges.map((e) => {
    if (e.from === from && e.to === to && (e.case ?? "") === (prevCase ?? "")) {
      const next = { ...e };
      if (nextCase) {
        next.case = nextCase;
      } else {
        delete next.case;
      }
      return next;
    }
    return e;
  });
}

// setEdgeCase retags one branch/classify edge's case on the draft.
export function setEdgeCase(from: string, to: string, prevCase: string | undefined, nextCase: string) {
  if (lockGuard("editing edge case")) return;
  draftWorkflow.update((wf) => {
    if (!wf) return wf;
    ensureGraph(wf);
    wf.graph.edges = applyEdgeCase(wf.graph.edges, from, to, prevCase, nextCase);
    return wf;
  });
}

// saveDraft writes the current draft to the backend, refreshes the
// validation report, and updates saveStatus.
//
// `silent: true` (used by the auto-save subscriber) suppresses the
// success toast so a steady stream of canvas edits doesn't flood the
// corner — the toolbar status text + validation chip still update.
// Failures always toast: silent operation must not hide real errors.
// Project per-node `_canvas.{x,y}` into the workflow-level
// `_canvas.positions[id]` map. The Go Node struct doesn't carry a
// `_canvas` field so the per-node positions get dropped on the
// JSON → YAML round-trip; the workflow-level Canvas map (which IS
// declared as `map[string]any`) round-trips intact. Run this before
// every save so node drags persist.
function flattenCanvasPositions(wf: Workflow): Workflow {
  const positions: Record<string, { x: number; y: number }> = {};
  for (const n of wf.graph?.nodes ?? []) {
    if (n._canvas) {
      positions[n.id] = { x: n._canvas.x ?? 0, y: n._canvas.y ?? 0 };
    }
  }
  const prev = ((wf as any)._canvas ?? {}) as Record<string, unknown>;
  const prevPositions = (prev.positions ?? {}) as Record<
    string,
    { x?: number; y?: number }
  >;
  // Triggers live in the existing positions map already (drag handler
  // writes there directly). Merge so we don't clobber trigger entries
  // when projecting nodes.
  const merged = { ...prevPositions, ...positions };
  return {
    ...wf,
    _canvas: { ...prev, positions: merged },
  } as Workflow;
}

// Returns the set of labels (or ids when label is blank) that more
// than one node shares — `[]` when clean. Used to short-circuit save
// before the backend rejects the workflow.
function findDuplicateLabels(wf: Workflow): string[] {
  const seen = new Map<string, number>();
  for (const n of wf.graph?.nodes ?? []) {
    const key = (n.label || n.id || "").trim();
    if (!key) continue;
    seen.set(key, (seen.get(key) ?? 0) + 1);
  }
  return [...seen.entries()].filter(([, c]) => c > 1).map(([k]) => k);
}

// Returns labels that don't match LABEL_RE so save can short-circuit.
// Backend's parse.Validate rejects on the same rule but only after the
// round-trip; surfacing here keeps the editor responsive.
function findInvalidLabels(wf: Workflow): string[] {
  const out: string[] = [];
  for (const n of wf.graph?.nodes ?? []) {
    if (n.label && !isValidLabel(n.label)) out.push(n.label);
  }
  for (const t of wf.triggers ?? []) {
    if (t.label && !isValidLabel(t.label)) out.push(t.label);
  }
  return out;
}

export async function saveDraft(opts: { silent?: boolean } = {}) {
  const wf = get(draftWorkflow);
  if (!wf) return;
  // Client-side dup label check — block before round-tripping the
  // backend so the user sees the conflict instantly. The Go
  // validator catches the same case but only after the disk write.
  const dups = findDuplicateLabels(wf);
  if (dups.length > 0) {
    saveStatus.set("failed");
    toastError(
      "Cannot save — duplicate labels",
      `Used by more than one node: ${dups.join(", ")}. Rename via the inspector.`,
    );
    return;
  }
  // Same client-side guard for the label format — backend's parse
  // validator enforces LABEL_RE (`^[a-z_][a-z0-9_]*$`); catching it
  // here means the inspector's inline error explains the rule before
  // the round-trip would reject.
  const bad = findInvalidLabels(wf);
  if (bad.length > 0) {
    saveStatus.set("failed");
    toastError(
      "Cannot save — invalid label format",
      `${bad.join(", ")} — ${LABEL_FORMAT_HINT}.`,
    );
    return;
  }
  const projected = flattenCanvasPositions(wf);
  saveStatus.set("saving");
  let res: Awaited<ReturnType<typeof workflowAPI.saveDraft>>;
  try {
    res = await workflowAPI.saveDraft(wf.id, projected);
  } catch (e) {
    saveStatus.set("failed");
    // 423 Locked has its own friendlier shape — the message tells the
    // operator exactly how to recover instead of dumping the URL the
    // backend mentions. Everything else surfaces the server's
    // {error: "..."} detail extracted by APIError.
    if (e instanceof APIError && e.status === 423) {
      toastError("Workflow is locked", "Unlock the canvas before saving edits.");
    } else {
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    }
    throw e;
  }
  saveStatus.set("saved");
  lastSavedAt.set(Date.now());
  // Validation rides on the same response — see spaWorkflowSave in
  // internal/tools/agents/spa_workflows.go.
  validationReport.set(decorateReport(res.validation));
  if (!opts.silent) {
    toastOk("Saved");
  }
}

export async function publish(message?: string) {
  const wf = get(draftWorkflow);
  if (!wf) return;
  // Cheap UI-side gate using the latest validation snapshot. The
  // backend gate is still authoritative — a stale UI state can
  // still hit the server and get back the "cannot publish" JSON,
  // which we surface via the regular catch path.
  const errCount = (get(validationReport)?.errors ?? []).length;
  if (errCount > 0) {
    toastError(
      "Cannot publish",
      `Fix ${errCount} validation ${errCount === 1 ? "error" : "errors"} first.`,
    );
    return;
  }
  try {
    await workflowAPI.publish(wf.id, message);
  } catch (e) {
    toastError(
      "Publish failed",
      e instanceof Error ? e.message : String(e),
    );
    throw e;
  }
  publishedWorkflow.set(structuredClone(wf));
  toastOk("Published");
}
