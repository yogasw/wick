<script lang="ts">
  // Canvas: absolute-positioned node cards laid out by node._canvas.{x,y}.
  // Drawflow integration deferred (Phase 4 of svelte-migration doc) —
  // this implementation owns drag, pan, marquee + edge SVG itself so the
  // initial port has zero JS-lib dependency. When we wire Drawflow back,
  // it mounts inside this component and the layout/positions feed into
  // its API rather than absolute `<div style>`.
  import { draftWorkflow, selectedNodeID, selectedNodeIDs, updateNode, addNode, removeNode, removeTrigger, disconnect, paletteOpen, detailNodeID, detailTriggerID, runStatusByNode } from "$lib/stores/editor";
  import { workflowAPI } from "$lib/api/workflow";
  import { componentFor } from "./nodes";
  import TriggerNode from "./nodes/TriggerNode.svelte";
  import ConfirmDialog from "$lib/components/shared/ConfirmDialog.svelte";
  import type { NodeType, Edge } from "$lib/types/workflow";

  let canvasEl: HTMLDivElement | undefined = $state();
  let pan = $state({ x: 0, y: 0 });
  let zoom = $state(1);
  let dragging = $state<{ id: string; offsetX: number; offsetY: number } | null>(null);

  // Auto fit-to-view once the workflow finishes loading. Compute the
  // bounding box of every node + trigger position, then pan + zoom so
  // the whole graph sits centred with a 60px margin. Matches the
  // legacy editor's `[Reset view]` button behaviour on initial load.
  let fitted = false;
  $effect(() => {
    const wf = $draftWorkflow;
    if (!wf || fitted || !canvasEl) return;
    const positions: { x: number; y: number }[] = [];
    for (const n of wf.graph?.nodes ?? []) {
      positions.push({ x: n._canvas?.x ?? 0, y: n._canvas?.y ?? 0 });
    }
    const positionsMap = ((wf as any)._canvas?.positions ?? {}) as Record<string, { x?: number; y?: number }>;
    for (const t of wf.triggers ?? []) {
      const p = positionsMap[t.id ?? ""];
      if (p) positions.push({ x: p.x ?? 0, y: p.y ?? 0 });
    }
    if (positions.length === 0) return;
    const minX = Math.min(...positions.map((p) => p.x));
    const minY = Math.min(...positions.map((p) => p.y));
    const maxX = Math.max(...positions.map((p) => p.x)) + 220; // include node width
    const maxY = Math.max(...positions.map((p) => p.y)) + 90;  // include node height
    const margin = 60;
    const rect = canvasEl.getBoundingClientRect();
    const targetZoom = Math.min(
      1,
      (rect.width - margin * 2) / (maxX - minX),
      (rect.height - margin * 2) / (maxY - minY),
    );
    zoom = Math.max(0.4, targetZoom);
    pan = {
      x: margin - minX * zoom + (rect.width - margin * 2 - (maxX - minX) * zoom) / 2,
      y: margin - minY * zoom + (rect.height - margin * 2 - (maxY - minY) * zoom) / 2,
    };
    fitted = true;
  });

  function ondrop(e: DragEvent) {
    e.preventDefault();
    if (!canvasEl) return;
    const rect = canvasEl.getBoundingClientRect();
    const x = (e.clientX - rect.left - pan.x) / zoom;
    const y = (e.clientY - rect.top - pan.y) / zoom;
    const nodeType = e.dataTransfer?.getData("application/x-wick-node-type") as NodeType | "";
    const triggerType = e.dataTransfer?.getData("application/x-wick-trigger-type");
    if (nodeType) {
      const id = `${nodeType}_${Math.random().toString(36).slice(2, 8)}`;
      addNode({ id, type: nodeType, _canvas: { x, y } });
      return;
    }
    if (triggerType) {
      // Spawn a Trigger entry — distinct from graph nodes. ID is a
      // uuid-ish so the canvas positions map can key it.
      const id = `trigger-${Math.random().toString(36).slice(2, 8)}`;
      draftWorkflow.update((wf) => {
        if (!wf) return wf;
        wf.triggers = [
          ...(wf.triggers ?? []),
          { id, type: triggerType as any },
        ];
        const canvas = ((wf as any)._canvas ?? {}) as any;
        if (!canvas.positions) canvas.positions = {};
        canvas.positions[id] = { x, y };
        (wf as any)._canvas = canvas;
        return wf;
      });
    }
  }

  // Drag origin distinguishes nodes (stored in graph.nodes[].
  // _canvas) from triggers (stored in workflow._canvas.positions[id]).
  // Both update reactively but write to different shapes.
  let dragKind = $state<"node" | "trigger" | null>(null);

  // Snap-to-align guides — drawn while a node is being dragged. The
  // guide lines appear when the dragged node's centre is within
  // `SNAP_THRESHOLD` pixels of another node's centre on the same axis;
  // the drag position is also pulled onto that axis so the user feels
  // a magnetic stick. Matches the legacy editor "auto-correction"
  // behaviour the user asked for.
  const SNAP_THRESHOLD = 8;
  const NODE_W = 220;
  const NODE_H = 90;
  let snapGuides = $state<{ x?: number; y?: number }>({});

  // Read all canvas positions (nodes + triggers) keyed by id —
  // returns centre coords for snap math.
  function snapCandidates(excludeID: string): { id: string; cx: number; cy: number }[] {
    const wf = $draftWorkflow;
    if (!wf) return [];
    const out: { id: string; cx: number; cy: number }[] = [];
    for (const n of wf.graph?.nodes ?? []) {
      if (n.id === excludeID) continue;
      const x = n._canvas?.x ?? 0;
      const y = n._canvas?.y ?? 0;
      out.push({ id: n.id, cx: x + NODE_W / 2, cy: y + NODE_H / 2 });
    }
    const positions = ((wf as any)._canvas?.positions ?? {}) as Record<string, { x: number; y: number }>;
    for (const t of wf.triggers ?? []) {
      if (!t.id || t.id === excludeID) continue;
      const p = positions[t.id] ?? { x: 0, y: 0 };
      out.push({ id: t.id, cx: p.x + NODE_W / 2, cy: p.y + NODE_H / 2 });
    }
    return out;
  }

  // ── Connect-by-drag ────────────────────────────────────────────────
  // Holds an in-flight connection while the user drags from one card's
  // output port to another card's input port. Trigger output → node
  // input writes back to `trigger.entry_node`; node output → node input
  // appends a new edge to `graph.edges`.
  let connecting = $state<{
    fromID: string;
    fromKind: "node" | "trigger" | "node-input";
    startX: number;
    startY: number;
  } | null>(null);
  let connectCursor = $state<{ x: number; y: number } | null>(null);

  // ── Context menu (right-click on node / trigger / edge) ─────────────
  // Floating menu pinned to mouse position. Currently exposes Delete
  // only; expand here when more per-target actions land. Target
  // discriminates so the same menu can act on three different shapes.
  type CtxTarget =
    | { kind: "node"; id: string }
    | { kind: "trigger"; id: string }
    | { kind: "edge"; from: string; to: string; caseKey?: string }
    | { kind: "trigger-edge"; triggerID: string };
  let ctxMenu = $state<{ x: number; y: number; target: CtxTarget } | null>(null);

  function openCtxMenu(e: MouseEvent, target: CtxTarget) {
    e.preventDefault();
    e.stopPropagation();
    ctxMenu = { x: e.clientX, y: e.clientY, target };
  }

  function closeCtxMenu() {
    ctxMenu = null;
  }

  function deleteCtxTarget() {
    const t = ctxMenu?.target;
    if (!t) return;
    switch (t.kind) {
      case "node":
        removeNode(t.id);
        break;
      case "trigger":
        removeTrigger(t.id);
        break;
      case "edge":
        disconnect(t.from, t.to, t.caseKey);
        break;
      case "trigger-edge":
        draftWorkflow.update((wf) => {
          if (!wf) return wf;
          wf.triggers = (wf.triggers ?? []).map((tr) =>
            tr.id === t.triggerID ? { ...tr, entry_node: undefined } : tr,
          );
          return wf;
        });
        break;
    }
    closeCtxMenu();
  }

  function startConnect(e: PointerEvent, fromID: string, fromKind: "node" | "trigger" | "node-input") {
    // Only left-button drags create connections. Right-click goes to
    // the context menu instead — otherwise the dashed connect line
    // gets orphaned because contextmenu suppresses the pointerup that
    // would normally clear `connecting`.
    if (e.button !== 0) return;
    e.stopPropagation();
    if (!canvasEl) return;
    const rect = canvasEl.getBoundingClientRect();
    const pos = positionFor(fromID, fromKind === "trigger" ? "trigger" : "node");
    // Output ports sit at the card bottom; input ports at the top.
    const startY = fromKind === "node-input"
      ? pos.y
      : cardBottomY(fromID, pos.y, fromKind === "trigger" ? 90 : 110);
    connecting = { fromID, fromKind, startX: pos.x + NODE_W / 2, startY };
    connectCursor = {
      x: (e.clientX - rect.left - pan.x) / zoom,
      y: (e.clientY - rect.top - pan.y) / zoom,
    };
    (e.target as Element).setPointerCapture?.(e.pointerId);
  }

  function positionFor(id: string, kind: "node" | "trigger"): { x: number; y: number } {
    const wf = $draftWorkflow!;
    if (kind === "node") {
      const n = wf.graph.nodes.find((nn) => nn.id === id);
      return { x: n?._canvas?.x ?? 0, y: n?._canvas?.y ?? 0 };
    }
    const p = ((wf as any)._canvas?.positions ?? {})[id];
    return { x: p?.x ?? 0, y: p?.y ?? 0 };
  }

  function onConnectMove(e: PointerEvent) {
    if (!connecting || !canvasEl) return;
    const rect = canvasEl.getBoundingClientRect();
    connectCursor = {
      x: (e.clientX - rect.left - pan.x) / zoom,
      y: (e.clientY - rect.top - pan.y) / zoom,
    };
  }

  function finishConnect(e: PointerEvent) {
    if (!connecting) return;
    // Hit-test: find a node whose input port (top-centre) is closest
    // to the cursor and within a generous radius.
    const wf = $draftWorkflow;
    if (!wf || !canvasEl || !connectCursor) {
      connecting = null;
      connectCursor = null;
      return;
    }
    const HIT_RADIUS = 60;
    let bestID: string | null = null;
    let bestDist = HIT_RADIUS;
    for (const n of wf.graph.nodes) {
      if (n.id === connecting.fromID) continue;
      const x = (n._canvas?.x ?? 0) + NODE_W / 2;
      const y = n._canvas?.y ?? 0;
      const dx = connectCursor.x - x;
      const dy = connectCursor.y - y;
      const d = Math.hypot(dx, dy);
      if (d < bestDist) {
        bestDist = d;
        bestID = n.id;
      }
    }
    if (bestID) {
      if (connecting.fromKind === "trigger") {
        // Trigger → node: write entry_node, not an edge.
        const triggerID = connecting.fromID;
        const target = bestID;
        draftWorkflow.update((current) => {
          if (!current) return current;
          current.triggers = (current.triggers ?? []).map((t) =>
            t.id === triggerID ? { ...t, entry_node: target } : t,
          );
          return current;
        });
      } else {
        // node-input → reverse direction (drop becomes the source).
        const from = connecting.fromKind === "node-input" ? bestID : connecting.fromID;
        const to = connecting.fromKind === "node-input" ? connecting.fromID : bestID;
        draftWorkflow.update((current) => {
          if (!current) return current;
          const dup = (current.graph.edges ?? []).some((edge) => edge.from === from && edge.to === to);
          if (!dup) {
            current.graph.edges = [...(current.graph.edges ?? []), { from, to }];
          }
          return current;
        });
      }
    }
    connecting = null;
    connectCursor = null;
    void e;
  }

  function applySnap(rawX: number, rawY: number, excludeID: string): { x: number; y: number } {
    const cx = rawX + NODE_W / 2;
    const cy = rawY + NODE_H / 2;
    let snappedCX = cx;
    let snappedCY = cy;
    let guideX: number | undefined;
    let guideY: number | undefined;
    for (const c of snapCandidates(excludeID)) {
      if (Math.abs(c.cx - cx) <= SNAP_THRESHOLD) {
        snappedCX = c.cx;
        guideX = c.cx;
      }
      if (Math.abs(c.cy - cy) <= SNAP_THRESHOLD) {
        snappedCY = c.cy;
        guideY = c.cy;
      }
    }
    snapGuides = { x: guideX, y: guideY };
    return { x: snappedCX - NODE_W / 2, y: snappedCY - NODE_H / 2 };
  }

  // For multi-drag: snapshot of starting positions of every selected
  // id so move applies a uniform delta. Cleared on pointerup.
  let multiDragStart = $state<Map<string, { x: number; y: number }> | null>(null);

  function startMultiDragSnapshot(rootID: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    const sel = $selectedNodeIDs;
    if (!sel.has(rootID) || sel.size <= 1) return;
    const snap = new Map<string, { x: number; y: number }>();
    const positions = ((wf as any)._canvas?.positions ?? {}) as Record<string, { x?: number; y?: number }>;
    for (const id of sel) {
      const n = wf.graph?.nodes?.find((nn) => nn.id === id);
      if (n) {
        snap.set(id, { x: n._canvas?.x ?? 0, y: n._canvas?.y ?? 0 });
        continue;
      }
      const p = positions[id];
      if (p) snap.set(id, { x: p.x ?? 0, y: p.y ?? 0 });
    }
    multiDragStart = snap;
  }

  function onnodepointerdown(e: PointerEvent, nodeID: string) {
    e.stopPropagation();
    const target = e.currentTarget as HTMLElement;
    const rect = target.getBoundingClientRect();
    dragging = { id: nodeID, offsetX: e.clientX - rect.left, offsetY: e.clientY - rect.top };
    dragKind = "node";
    // Shift-click adds to the selection; plain click resets to one.
    if (e.shiftKey) {
      selectedNodeIDs.update((s) => {
        const next = new Set(s);
        next.add(nodeID);
        return next;
      });
    } else if (!$selectedNodeIDs.has(nodeID)) {
      selectedNodeIDs.set(new Set([nodeID]));
    }
    selectedNodeID.set(nodeID);
    startMultiDragSnapshot(nodeID);
    (e.target as Element).setPointerCapture?.(e.pointerId);
  }

  function ontriggerpointerdown(e: PointerEvent, triggerID: string) {
    e.stopPropagation();
    const target = e.currentTarget as HTMLElement;
    const rect = target.getBoundingClientRect();
    dragging = { id: triggerID, offsetX: e.clientX - rect.left, offsetY: e.clientY - rect.top };
    dragKind = "trigger";
    if (e.shiftKey) {
      selectedNodeIDs.update((s) => {
        const next = new Set(s);
        next.add(triggerID);
        return next;
      });
    } else if (!$selectedNodeIDs.has(triggerID)) {
      selectedNodeIDs.set(new Set([triggerID]));
    }
    selectedNodeID.set(triggerID);
    startMultiDragSnapshot(triggerID);
    (e.target as Element).setPointerCapture?.(e.pointerId);
  }

  function onpointermove(e: PointerEvent) {
    if (!dragging || !canvasEl) return;
    const rect = canvasEl.getBoundingClientRect();
    const rawX = (e.clientX - rect.left - pan.x - dragging.offsetX) / zoom;
    const rawY = (e.clientY - rect.top - pan.y - dragging.offsetY) / zoom;
    const { x, y } = applySnap(rawX, rawY, dragging.id);
    // Multi-drag: every selected card moves by the same delta the
    // root one travelled. Falls back to single-card drag when only
    // one is selected (or the user dragged outside the selection).
    if (multiDragStart && multiDragStart.has(dragging.id)) {
      const rootStart = multiDragStart.get(dragging.id)!;
      const dx = x - rootStart.x;
      const dy = y - rootStart.y;
      draftWorkflow.update((wf) => {
        if (!wf) return wf;
        const canvas = ((wf as any)._canvas ?? {}) as any;
        if (!canvas.positions) canvas.positions = {};
        for (const [id, start] of multiDragStart!) {
          const nx = start.x + dx;
          const ny = start.y + dy;
          const n = wf.graph?.nodes?.find((nn) => nn.id === id);
          if (n) {
            n._canvas = { x: nx, y: ny };
          } else {
            canvas.positions[id] = { x: nx, y: ny };
          }
        }
        (wf as any)._canvas = canvas;
        return wf;
      });
      return;
    }
    if (dragKind === "trigger") {
      draftWorkflow.update((wf) => {
        if (!wf) return wf;
        const canvas = ((wf as any)._canvas ?? {}) as any;
        if (!canvas.positions) canvas.positions = {};
        canvas.positions[dragging!.id] = { x, y };
        (wf as any)._canvas = canvas;
        return wf;
      });
    } else {
      updateNode(dragging.id, { _canvas: { x, y } });
    }
  }

  function onpointerup() {
    dragging = null;
    dragKind = null;
    snapGuides = {};
    multiDragStart = null;
  }

  // Marquee selection — drag on empty canvas paints a rectangle, and
  // any node/trigger whose centre falls inside lands in the selection
  // set. Mirrors the legacy editor's "shift-drag to select multiple"
  // — accepts plain drag because the canvas background isn't useful
  // for anything else.
  let marquee = $state<{ startX: number; startY: number; endX: number; endY: number } | null>(null);

  function oncanvaspointerdown(e: PointerEvent) {
    if (e.target !== canvasEl) return;
    if (spaceHeld) return; // space+drag pans; no marquee
    if (!canvasEl) return;
    selectedNodeID.set(null);
    selectedNodeIDs.set(new Set());
    const rect = canvasEl.getBoundingClientRect();
    const x = (e.clientX - rect.left - pan.x) / zoom;
    const y = (e.clientY - rect.top - pan.y) / zoom;
    marquee = { startX: x, startY: y, endX: x, endY: y };
    (e.target as Element).setPointerCapture?.(e.pointerId);
  }

  function onMarqueeMove(e: PointerEvent) {
    if (!marquee || !canvasEl) return;
    const rect = canvasEl.getBoundingClientRect();
    marquee.endX = (e.clientX - rect.left - pan.x) / zoom;
    marquee.endY = (e.clientY - rect.top - pan.y) / zoom;
  }

  function onMarqueeUp() {
    if (!marquee) return;
    const wf = $draftWorkflow;
    const m = marquee;
    marquee = null;
    if (!wf) return;
    const minX = Math.min(m.startX, m.endX);
    const minY = Math.min(m.startY, m.endY);
    const maxX = Math.max(m.startX, m.endX);
    const maxY = Math.max(m.startY, m.endY);
    if (Math.abs(maxX - minX) < 6 && Math.abs(maxY - minY) < 6) return; // tap, not drag
    const next = new Set<string>();
    const positions = ((wf as any)._canvas?.positions ?? {}) as Record<string, { x?: number; y?: number }>;
    for (const n of wf.graph?.nodes ?? []) {
      const cx = (n._canvas?.x ?? 0) + NODE_W / 2;
      const cy = (n._canvas?.y ?? 0) + 50;
      if (cx >= minX && cx <= maxX && cy >= minY && cy <= maxY) next.add(n.id);
    }
    for (const t of wf.triggers ?? []) {
      if (!t.id) continue;
      const p = positions[t.id] ?? { x: 0, y: 0 };
      const cx = (p.x ?? 0) + NODE_W / 2;
      const cy = (p.y ?? 0) + 50;
      if (cx >= minX && cx <= maxX && cy >= minY && cy <= maxY) next.add(t.id);
    }
    selectedNodeIDs.set(next);
  }

  function onwheel(e: WheelEvent) {
    if (!canvasEl) return;
    // Ctrl/Cmd + wheel → zoom anchored at the cursor. Plain wheel +
    // touchpad two-finger gesture → pan, mirroring the Figma / n8n
    // canvas convention (the browser emits deltaX/deltaY for both
    // axes when a trackpad scrolls).
    if (e.ctrlKey || e.metaKey) {
      e.preventDefault();
      const rect = canvasEl.getBoundingClientRect();
      const rawFactor = e.deltaY < 0 ? 1.1 : 1 / 1.1;
      const nextZoom = Math.max(0.25, Math.min(2.5, zoom * rawFactor));
      const factor = nextZoom / zoom;
      const cx = e.clientX - rect.left;
      const cy = e.clientY - rect.top;
      pan = {
        x: cx - (cx - pan.x) * factor,
        y: cy - (cy - pan.y) * factor,
      };
      zoom = nextZoom;
      return;
    }
    // Plain scroll — pan canvas. Browser default would scroll the
    // page; preventDefault keeps the gesture inside the canvas.
    e.preventDefault();
    pan = { x: pan.x - e.deltaX, y: pan.y - e.deltaY };
  }

  // Spacebar held + drag → pan canvas (alternative to two-finger
  // gesture for mice without scroll wheel). Tracks pointer drag
  // delta while space is down.
  let spaceHeld = $state(false);
  let panDrag = $state<{ startX: number; startY: number; startPanX: number; startPanY: number } | null>(null);

  function onSpaceKeydown(e: KeyboardEvent) {
    if (e.code === "Space" && !spaceHeld) {
      const tag = (document.activeElement?.tagName ?? "").toLowerCase();
      if (tag === "input" || tag === "textarea") return;
      e.preventDefault();
      spaceHeld = true;
    }
  }
  function onSpaceKeyup(e: KeyboardEvent) {
    if (e.code === "Space") {
      spaceHeld = false;
      panDrag = null;
    }
  }
  function onPanDragStart(e: PointerEvent) {
    if (!spaceHeld) return;
    if (e.target !== canvasEl) return;
    panDrag = { startX: e.clientX, startY: e.clientY, startPanX: pan.x, startPanY: pan.y };
    (e.target as Element).setPointerCapture?.(e.pointerId);
  }
  function onPanDragMove(e: PointerEvent) {
    if (!panDrag) return;
    pan = {
      x: panDrag.startPanX + (e.clientX - panDrag.startX),
      y: panDrag.startPanY + (e.clientY - panDrag.startY),
    };
  }

  let confirmDeleteNode = $state(false);
  function requestDeleteSelected() {
    if ($selectedNodeIDs.size > 0 || $selectedNodeID) confirmDeleteNode = true;
  }
  function confirmDeleteSelected() {
    // Delete the whole multi-selection if it exists, otherwise the
    // single highlighted card. Triggers and nodes are mixed into one
    // pass — each id is checked against both stores.
    const ids = $selectedNodeIDs.size > 0
      ? [...$selectedNodeIDs]
      : ($selectedNodeID ? [$selectedNodeID] : []);
    if (ids.length === 0) {
      confirmDeleteNode = false;
      return;
    }
    for (const id of ids) {
      // Selection can point to either a node or a trigger — try
      // each. Triggers live on workflow.triggers so we mutate the
      // draftWorkflow store directly; nodes flow through removeNode
      // (which also prunes touching edges).
      const wf = $draftWorkflow;
      const isTrigger = !!wf?.triggers?.some((t) => t.id === id);
      if (isTrigger) {
        draftWorkflow.update((cur) => {
          if (!cur) return cur;
          cur.triggers = (cur.triggers ?? []).filter((t) => t.id !== id);
          const canvas = ((cur as any)._canvas ?? {}) as any;
          if (canvas.positions) delete canvas.positions[id];
          return cur;
        });
      } else {
        removeNode(id);
      }
    }
    selectedNodeID.set(null);
    selectedNodeIDs.set(new Set());
    confirmDeleteNode = false;
  }

  // Auto-format — layered top-down layout. BFS from triggers, group
  // nodes by depth, sort each level by current x position to keep
  // user intent within a level, then snap to a grid:
  //   trigger row → y = 0
  //   level 1     → y = 220
  //   level 2     → y = 440
  // x within a level is centred around midpoint, 260px apart.
  function autoFormat() {
    const wf = $draftWorkflow;
    if (!wf) return;
    const nodes = wf.graph?.nodes ?? [];
    const edges = wf.graph?.edges ?? [];
    const triggers = wf.triggers ?? [];

    const children = new Map<string, string[]>();
    const indeg = new Map<string, number>();
    for (const n of nodes) {
      indeg.set(n.id, 0);
      children.set(n.id, []);
    }
    for (const t of triggers) {
      if (t.id) {
        children.set(t.id, []);
        indeg.set(t.id, 0);
      }
    }
    for (const e of edges) {
      children.get(e.from)?.push(e.to);
      indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
    }
    for (const t of triggers) {
      if (t.id && t.entry_node) {
        children.get(t.id)?.push(t.entry_node);
        indeg.set(t.entry_node, (indeg.get(t.entry_node) ?? 0) + 1);
      }
    }

    const depth = new Map<string, number>();
    const queue: string[] = [];
    for (const [id, n] of indeg) {
      if (n === 0) {
        depth.set(id, 0);
        queue.push(id);
      }
    }
    while (queue.length) {
      const cur = queue.shift()!;
      const d = depth.get(cur)!;
      for (const child of children.get(cur) ?? []) {
        const next = d + 1;
        if (!depth.has(child) || depth.get(child)! < next) depth.set(child, next);
        queue.push(child);
      }
    }

    const byLevel = new Map<number, string[]>();
    for (const [id, d] of depth) {
      const arr = byLevel.get(d) ?? [];
      arr.push(id);
      byLevel.set(d, arr);
    }

    const COL_GAP = 260;
    const ROW_GAP = 220;
    const ORIGIN_X = 420;
    const ORIGIN_Y = 60;

    const oldPos = ((wf as any)._canvas?.positions ?? {}) as Record<string, { x: number; y: number }>;
    function currentX(id: string): number {
      const n = nodes.find((nn) => nn.id === id);
      if (n?._canvas?.x !== undefined) return n._canvas.x;
      return oldPos[id]?.x ?? 0;
    }

    draftWorkflow.update((current) => {
      if (!current) return current;
      const canvas = ((current as any)._canvas ?? {}) as any;
      canvas.positions = { ...(canvas.positions ?? {}) };
      const levels = [...byLevel.keys()].sort((a, b) => a - b);
      for (const lvl of levels) {
        const ids = byLevel.get(lvl)!;
        ids.sort((a, b) => currentX(a) - currentX(b));
        const totalWidth = (ids.length - 1) * COL_GAP;
        const startX = ORIGIN_X - totalWidth / 2;
        ids.forEach((id, i) => {
          const x = startX + i * COL_GAP;
          const y = ORIGIN_Y + lvl * ROW_GAP;
          canvas.positions[id] = { x, y };
          const n = current.graph.nodes.find((nn) => nn.id === id);
          if (n) n._canvas = { x, y };
        });
      }
      (current as any)._canvas = canvas;
      return current;
    });
  }

  let triggerPickerOpen = $state(false);
  function toggleTriggerPicker() {
    const wf = $draftWorkflow;
    if (!wf) return;
    // One trigger → no need for a popup, fire directly. Multiple
    // triggers (cron + slack, etc.) need disambiguation — show the
    // picker matching the legacy "Pick trigger to fire" pattern.
    const triggers = wf.triggers ?? [];
    if (triggers.length <= 1) {
      void executeTrigger(triggers[0]?.id);
      return;
    }
    triggerPickerOpen = !triggerPickerOpen;
  }
  async function executeTrigger(triggerID?: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    triggerPickerOpen = false;
    try {
      await workflowAPI.runNow(wf.id);
      void triggerID; // backend currently picks the first manual trigger
    } catch (e) {
      console.error("execute workflow failed:", e);
    }
  }

  function onkeydown(e: KeyboardEvent) {
    if ((e.key === "Delete" || e.key === "Backspace") && $selectedNodeID) {
      const tag = (document.activeElement?.tagName ?? "").toLowerCase();
      if (tag === "input" || tag === "textarea") return;
      e.preventDefault();
      requestDeleteSelected();
    }
  }

  // Edge geometry. SVG below the nodes layer; coords absolute in the
  // node-coordinate space then scaled by the transform.
  // Per-card measured height. Populated by bind:clientHeight on each
  // wrapper so edge endpoints land on the *actual* port positions
  // instead of a hard-coded guess (TriggerNode is short, datatable
  // nodes with case pills are tall, etc.). Keyed by id; falls back
  // to a sensible default while measurement is still pending.
  const cardHeights = $state<Record<string, number>>({});
  function cardBottomY(id: string, posY: number, fallback: number): number {
    const h = cardHeights[id];
    return posY + (h || fallback);
  }

  // Edge endpoints — used by both edgePath() and the mid-dot helper
  // so the dot lands exactly on the curve regardless of node offset.
  function nodeEdgePoints(fromID: string, toID: string): { ax: number; ay: number; bx: number; by: number } | null {
    const wf = $draftWorkflow;
    if (!wf?.graph?.nodes) return null;
    const from = wf.graph.nodes.find((n) => n.id === fromID);
    const to = wf.graph.nodes.find((n) => n.id === toID);
    if (!from || !to) return null;
    return {
      ax: (from._canvas?.x ?? 0) + NODE_W / 2,
      ay: cardBottomY(from.id, from._canvas?.y ?? 0, 110),
      bx: (to._canvas?.x ?? 0) + NODE_W / 2,
      by: to._canvas?.y ?? 0,
    };
  }
  function triggerEdgePoints(triggerID: string, entryNodeID: string): { ax: number; ay: number; bx: number; by: number } | null {
    const wf = $draftWorkflow;
    if (!wf?.graph?.nodes) return null;
    const positions = ((wf as any)._canvas?.positions ?? {}) as Record<string, { x?: number; y?: number }>;
    const from = positions[triggerID];
    const to = wf.graph.nodes.find((n) => n.id === entryNodeID);
    if (!from || !to) return null;
    return {
      ax: (from.x ?? 0) + NODE_W / 2,
      ay: cardBottomY(triggerID, from.y ?? 0, 90),
      bx: (to._canvas?.x ?? 0) + NODE_W / 2,
      by: to._canvas?.y ?? 0,
    };
  }
  // Single smooth cubic. Control points pulled vertically away from
  // each endpoint by half the y-gap so the curve enters/exits each
  // node perpendicular to its port. No mid vertex, so the path has
  // no kink — the yellow indicator is painted as a separate
  // `<circle>` at the computed midpoint instead.
  function bezier(p: { ax: number; ay: number; bx: number; by: number }): string {
    // Control-point vertical offset scales with BOTH x- and y-gap so
    // the curve opens up smoothly when the source/target sit far
    // apart on the x-axis (otherwise the line bends sharply through
    // the gap and looks "broken").
    const dx = Math.abs(p.bx - p.ax);
    const dy = Math.max(40, Math.max(dx, Math.abs(p.by - p.ay)) / 2);
    return `M ${p.ax} ${p.ay} C ${p.ax} ${p.ay + dy}, ${p.bx} ${p.by - dy}, ${p.bx} ${p.by}`;
  }
  function midPoint(p: { ax: number; ay: number; bx: number; by: number }): { x: number; y: number } {
    // Cubic with control points (ax, ay+dy) + (bx, by-dy) — at t=0.5
    // the point is exactly the midpoint between (ax,ay) and (bx,by)
    // along x AND along y. Cheap closed form.
    return { x: (p.ax + p.bx) / 2, y: (p.ay + p.by) / 2 };
  }
  function triggerEdgePath(triggerID: string, entryNodeID: string): string | null {
    const p = triggerEdgePoints(triggerID, entryNodeID);
    return p ? bezier(p) : null;
  }

  function edgePath(e: Edge): string | null {
    const p = nodeEdgePoints(e.from, e.to);
    return p ? bezier(p) : null;
  }
</script>

<svelte:window
  onpointermove={(e) => { onpointermove(e); onConnectMove(e); onPanDragMove(e); onMarqueeMove(e); }}
  onpointerup={(e) => { onpointerup(); finishConnect(e); panDrag = null; onMarqueeUp(); }}
  onpointercancel={() => { connecting = null; connectCursor = null; }}
  onpointerdown={(e) => {
    // Backup cancel — if a connect drag was orphaned (pointer left the
    // window mid-drag, browser ate the pointerup, etc.), the next
    // pointerdown anywhere clears it. Don't fire when the user starts
    // a fresh connect from a port (handled by stopPropagation in
    // startConnect → this listener never sees those events).
    if (connecting && e.button === 0) {
      connecting = null;
      connectCursor = null;
    }
  }}
  onkeydown={(e) => {
    onkeydown(e); onSpaceKeydown(e);
    // Esc cancels an in-flight connect (orphaned dashed line on canvas)
    // and closes the right-click context menu.
    if (e.key === "Escape") {
      connecting = null; connectCursor = null;
      ctxMenu = null;
    }
  }}
  onkeyup={onSpaceKeyup}
  onclick={() => { if (ctxMenu) ctxMenu = null; }}
/>

<div
  class="flex-1 relative overflow-hidden wf-canvas-bg"
  class:cursor-grab={spaceHeld && !panDrag}
  class:cursor-grabbing={panDrag}
  ondragover={(e) => e.preventDefault()}
  ondrop={ondrop}
  onwheel={onwheel}
  bind:this={canvasEl}
  onpointerdown={(e) => { onPanDragStart(e); oncanvaspointerdown(e); }}
  role="presentation"
>
  <div
    class="absolute inset-0"
    style="transform: translate({pan.x}px,{pan.y}px) scale({zoom}); transform-origin: 0 0;"
  >
    {#if $draftWorkflow?.graph}
      <svg
        class="absolute inset-0 pointer-events-none overflow-visible"
        style="width:1px;height:1px;left:0;top:0;"
      >
        <!-- Mid-edge arrow marker — matches the legacy editor's
             yellow indicator on each connection. Drawflow puts the
             arrow at the curve midpoint (createCurvature override)
             because end-tip arrows pile up when multiple edges fan in. -->
        <defs>
          <!-- Two markers placed on the same mid-vertex: the legacy
               editor's arrow head (▽) for direction + a yellow dot for
               quick eye-tracking. SVG can only attach a single marker
               per slot, so we use marker-mid for the dot and a
               manual `<polygon>` overlay further down for the
               arrowhead. The `wf-arrow` def keeps backward compat
               with anything referencing it directly. -->
          <marker id="wf-arrow" viewBox="0 0 10 10" refX="5" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
            <circle cx="5" cy="5" r="4" fill="#facc15" />
          </marker>
          <marker id="wf-arrowhead" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
            <path d="M0,0 L10,5 L0,10 z" fill="#9aa3b2" />
          </marker>
        </defs>
        <!-- Trigger → entry_node edges live on the Trigger object,
             not in graph.edges. Render them with the same path
             generator so the user sees the same yellow-dot indicator
             on every connection regardless of origin. -->
        {#each $draftWorkflow.triggers ?? [] as trig}
          {#if trig.id && trig.entry_node}
            {@const pts = triggerEdgePoints(trig.id, trig.entry_node)}
            {#if pts}
              {@const mid = midPoint(pts)}
              <path d={bezier(pts)} fill="none" stroke="currentColor" stroke-width="2" marker-end="url(#wf-arrowhead)" class="text-slate-400 dark:text-slate-600" />
              <!-- Wide invisible hit path widens right-click target — bare
                   stroke-width=2 is too thin to land on reliably. -->
              <path
                d={bezier(pts)}
                fill="none"
                stroke="transparent"
                stroke-width="14"
                style="pointer-events: stroke; cursor: context-menu;"
                oncontextmenu={(e) => openCtxMenu(e, { kind: "trigger-edge", triggerID: trig.id! })}
              />
              <circle cx={mid.x} cy={mid.y} r="4" fill="#facc15" />
            {/if}
          {/if}
        {/each}
        {#each $draftWorkflow.graph.edges ?? [] as e}
          {@const pts = nodeEdgePoints(e.from, e.to)}
          {#if pts}
            {@const mid = midPoint(pts)}
            <path d={bezier(pts)} fill="none" stroke="currentColor" stroke-width="2" marker-end="url(#wf-arrowhead)" class="text-slate-400 dark:text-slate-600" />
            <path
              d={bezier(pts)}
              fill="none"
              stroke="transparent"
              stroke-width="14"
              style="pointer-events: stroke; cursor: context-menu;"
              oncontextmenu={(ev) => openCtxMenu(ev, { kind: "edge", from: e.from, to: e.to, caseKey: e.case })}
            />
            <circle cx={mid.x} cy={mid.y} r="4" fill="#facc15" />
            {#if e.case}
              <text class="text-[10px] fill-slate-500">
                <textPath href={`#edge-${e.from}-${e.to}-${e.case}`}>{e.case}</textPath>
              </text>
            {/if}
          {/if}
        {/each}
      </svg>

      {#each $draftWorkflow.graph.nodes ?? [] as node (node.id)}
        {@const Comp = componentFor(node.type)}
        {@const status = $runStatusByNode[node.id]}
        <div
          class="absolute"
          style="left: {node._canvas?.x ?? 0}px; top: {node._canvas?.y ?? 0}px;"
          onpointerdown={(e) => onnodepointerdown(e, node.id)}
          ondblclick={() => detailNodeID.set(node.id)}
          oncontextmenu={(e) => openCtxMenu(e, { kind: "node", id: node.id })}
          role="presentation"
          bind:clientHeight={cardHeights[node.id]}
        >
          <Comp
            node={node}
            selected={$selectedNodeIDs.has(node.id) || $selectedNodeID === node.id}
            running={status === "running"}
            errored={status === "failed"}
            onselect={() => selectedNodeID.set(node.id)}
          />
          <!-- Output port — drag from here to another node's body to
               create an edge. Transparent overlay sits on BaseNode's
               bottom-centre white circle. -->
          <button
            class="absolute left-1/2 -translate-x-1/2 -bottom-[10px] h-5 w-5 rounded-full cursor-crosshair opacity-0 hover:opacity-100 transition-opacity"
            onpointerdown={(e) => startConnect(e, node.id, "node")}
            title="Drag to connect (output)"
            aria-label="Connect output"
            style="background:rgba(250,204,21,0.4)"
          ></button>
          <!-- Input port hit-target. Drag from here = REVERSE connect
               (creates an edge from the dropped node TO this one).
               Matches legacy editor where both directions worked. -->
          <button
            class="absolute left-1/2 -translate-x-1/2 -top-[10px] h-5 w-5 rounded-full cursor-crosshair opacity-0 hover:opacity-100 transition-opacity"
            onpointerdown={(e) => startConnect(e, node.id, "node-input")}
            title="Drag to connect (input)"
            aria-label="Connect input"
            style="background:rgba(250,204,21,0.4)"
          ></button>
          {#if status === "success"}
            <div class="absolute -top-1 -left-1 h-4 w-4 rounded-full bg-emerald-500 text-white text-[10px] flex items-center justify-center shadow">✓</div>
          {:else if status === "failed"}
            <div class="absolute -top-1 -left-1 h-4 w-4 rounded-full bg-rose-500 text-white text-[10px] flex items-center justify-center shadow">✗</div>
          {/if}
        </div>
      {/each}

      <!-- Snap-to-align guide lines while dragging. Dashed indigo
           strokes on the matching axis so the user feels exactly when
           the drag locks onto another node's centre. SVG bounds are
           huge + overflow:visible so the line keeps painting even
           when the canvas is panned/zoomed far from origin. -->
      {#if snapGuides.x !== undefined}
        <svg class="absolute inset-0 pointer-events-none overflow-visible" style="width:1px;height:1px;">
          <line x1={snapGuides.x} y1={-100000} x2={snapGuides.x} y2={100000} stroke="#a78bfa" stroke-width="1" stroke-dasharray="4 4" vector-effect="non-scaling-stroke" />
        </svg>
      {/if}
      {#if snapGuides.y !== undefined}
        <svg class="absolute inset-0 pointer-events-none overflow-visible" style="width:1px;height:1px;">
          <line x1={-100000} y1={snapGuides.y} x2={100000} y2={snapGuides.y} stroke="#a78bfa" stroke-width="1" stroke-dasharray="4 4" vector-effect="non-scaling-stroke" />
        </svg>
      {/if}

      <!-- Marquee selection rectangle (drag on empty canvas). -->
      {#if marquee}
        {@const minX = Math.min(marquee.startX, marquee.endX)}
        {@const minY = Math.min(marquee.startY, marquee.endY)}
        {@const w = Math.abs(marquee.endX - marquee.startX)}
        {@const h = Math.abs(marquee.endY - marquee.startY)}
        <div
          class="absolute pointer-events-none border border-emerald-400 bg-emerald-400/10"
          style="left:{minX}px; top:{minY}px; width:{w}px; height:{h}px;"
        ></div>
      {/if}

      <!-- In-progress connection preview while user drags from a port. -->
      {#if connecting && connectCursor}
        <svg class="absolute inset-0 w-[8000px] h-[8000px] pointer-events-none">
          <line
            x1={connecting.startX}
            y1={connecting.startY}
            x2={connectCursor.x}
            y2={connectCursor.y}
            stroke="#facc15"
            stroke-width="2"
            stroke-dasharray="6 4"
          />
        </svg>
      {/if}

      <!-- Triggers render as cards too. Positions come from the same
           `workflow._canvas.positions` map but keyed by trigger.id; the
           hydrate pass in `loadWorkflow` doesn't copy these onto the
           trigger object, so look them up inline. -->
      {#each $draftWorkflow.triggers ?? [] as trig (trig.id ?? trig.type)}
        {@const pos = ($draftWorkflow as any)._canvas?.positions?.[trig.id ?? ""] ?? { x: 60, y: 60 }}
        <div
          class="absolute"
          style="left: {pos.x}px; top: {pos.y}px;"
          onpointerdown={(e) => ontriggerpointerdown(e, trig.id ?? "")}
          ondblclick={() => trig.id && detailTriggerID.set(trig.id)}
          oncontextmenu={(e) => trig.id && openCtxMenu(e, { kind: "trigger", id: trig.id })}
          role="presentation"
          bind:clientHeight={cardHeights[trig.id ?? ""]}
        >
          <TriggerNode
            trigger={trig}
            selected={(trig.id ? $selectedNodeIDs.has(trig.id) : false) || $selectedNodeID === trig.id}
            onselect={() => selectedNodeID.set(trig.id ?? null)}
          />
          <!-- Trigger output port — transparent hit target over
               BaseNode's white circle (BaseNode is shared with regular
               nodes, so we don't re-draw the port handle here). -->
          <button
            class="absolute left-1/2 -translate-x-1/2 -bottom-[10px] h-5 w-5 rounded-full cursor-crosshair opacity-0 hover:opacity-100 transition-opacity"
            onpointerdown={(e) => startConnect(e, trig.id ?? "", "trigger")}
            title="Drag to connect to the entry node"
            aria-label="Connect trigger"
            style="background:rgba(250,204,21,0.4)"
          ></button>
        </div>
      {/each}
    {:else}
      <div class="absolute inset-0 flex items-center justify-center text-black-500 dark:text-white-700 text-sm">
        Load a workflow to start editing.
      </div>
    {/if}
  </div>

  <!-- Top-right: add node + search. Matches the legacy editor toolbar
       overlay. Search is a stub for now; it'll wire to a fuzzy filter
       across node ids + types once the palette grows enough to need it. -->
  <div class="absolute top-3 right-3 flex flex-col gap-1.5">
    <button
      class="h-9 w-9 rounded-full bg-emerald-500 hover:bg-emerald-600 text-white shadow flex items-center justify-center text-lg font-semibold"
      onclick={() => paletteOpen.update(v => !v)}
      title="Add node"
      aria-label="Add node"
    >+</button>
    <button
      class="h-9 w-9 rounded-full bg-slate-700/80 hover:bg-slate-700 text-white shadow flex items-center justify-center"
      title="Search nodes (Ctrl+K)"
      aria-label="Search nodes"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>
    </button>
  </div>

  <!-- Left-center vertical controls — match the legacy lock / auto-layout
       / fullscreen / zoom column. Lock + auto-layout are stubs (UX
       parity for now; behavior lands in the next pass). -->
  <div class="absolute left-3 top-1/2 -translate-y-1/2 flex flex-col gap-1.5">
    <button class="h-9 w-9 rounded bg-slate-800/80 text-amber-400 shadow flex items-center justify-center" title="Lock canvas" aria-label="Lock canvas">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
    </button>
    <button class="h-9 w-9 rounded bg-slate-800/80 hover:bg-slate-800 text-slate-100 shadow flex items-center justify-center" title="Auto-format (layered T→B)" aria-label="Auto-format" onclick={autoFormat}>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v18M5 10l7 7 7-7"/></svg>
    </button>
    <button class="h-9 w-9 rounded bg-slate-800/80 text-slate-100 shadow flex items-center justify-center" title="Fit to view" aria-label="Fit to view" onclick={() => { zoom = 1; pan = { x: 0, y: 0 }; }}>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 3 21 3 21 9"/><polyline points="9 21 3 21 3 15"/><line x1="21" y1="3" x2="14" y2="10"/><line x1="3" y1="21" x2="10" y2="14"/></svg>
    </button>
    <button class="h-9 w-9 rounded bg-slate-800/80 text-slate-100 shadow flex items-center justify-center text-base" onclick={() => (zoom = Math.max(0.25, zoom / 1.1))} title="Zoom out" aria-label="Zoom out">−</button>
    <button class="h-9 w-9 rounded bg-slate-800/80 text-slate-100 shadow flex items-center justify-center text-base" onclick={() => (zoom = Math.min(2.5, zoom * 1.1))} title="Zoom in" aria-label="Zoom in">+</button>
  </div>

  <!-- Bottom-center: Execute workflow CTA + trigger picker popup. -->
  <div class="absolute bottom-4 left-1/2 -translate-x-1/2 flex flex-col items-center gap-2">
    {#if triggerPickerOpen}
      <div class="rounded-lg bg-slate-900/95 border border-slate-700 shadow-xl p-3 min-w-[280px] text-xs">
        <div class="text-[10px] uppercase tracking-wider text-slate-500 mb-2">Pick trigger to fire</div>
        {#each $draftWorkflow?.triggers ?? [] as t}
          <button
            class="w-full flex items-center gap-2 px-2 py-2 rounded text-left hover:bg-slate-800 text-slate-100"
            onclick={() => executeTrigger(t.id)}
          >
            <span class="px-1.5 py-0.5 rounded bg-slate-700 text-[10px] uppercase tracking-wider">{t.type}</span>
            <span class="font-mono text-[11px] truncate flex-1">{t.id ?? "—"}</span>
            <span class="text-slate-500">→</span>
            <span class="font-mono text-[10px] text-slate-500 truncate">{t.entry_node ?? "?"}</span>
          </button>
        {/each}
        {#if ($draftWorkflow?.triggers ?? []).length === 0}
          <p class="text-slate-500 italic px-2 py-1">No triggers defined.</p>
        {/if}
      </div>
    {/if}
    <button
      class="flex items-center gap-2 px-5 py-2 rounded-full bg-rose-500 hover:bg-rose-600 text-white text-sm font-semibold shadow-lg"
      onclick={toggleTriggerPicker}
      title="Execute workflow"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M10 2v7.3M14 9.3V2M8.5 2h7M14 9.3a6.5 6.5 0 1 1-4 0"/></svg>
      Execute workflow
      <span class="text-white/80 text-xs">▾</span>
    </button>
  </div>

  <!-- Bottom-right: zoom badge. -->
  <div class="absolute bottom-4 right-4 text-[10px] text-slate-400 tabular-nums select-none">
    {Math.round(zoom * 100)}%
  </div>
</div>

<ConfirmDialog
  open={confirmDeleteNode}
  title="Delete node?"
  body="The node and every edge that touches it will be removed from the draft."
  confirmLabel="Delete"
  destructive
  onConfirm={confirmDeleteSelected}
  onCancel={() => (confirmDeleteNode = false)}
/>

{#if ctxMenu}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed z-[80] min-w-[140px] rounded-md border border-slate-200 dark:border-slate-700
           bg-white dark:bg-slate-800 shadow-lg py-1 text-sm"
    style="left: {ctxMenu.x}px; top: {ctxMenu.y}px;"
    role="menu"
    onclick={(e) => e.stopPropagation()}
    oncontextmenu={(e) => e.preventDefault()}
  >
    <button
      type="button"
      class="w-full text-left px-3 py-1.5 flex items-center gap-2 text-rose-600 dark:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-900/30"
      onclick={deleteCtxTarget}
    >
      <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24"
           fill="none" stroke="currentColor" stroke-width="2"
           stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <polyline points="3 6 5 6 21 6"></polyline>
        <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"></path>
        <path d="M10 11v6"></path>
        <path d="M14 11v6"></path>
        <path d="M9 6V4a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v2"></path>
      </svg>
      <span>
        {#if ctxMenu.target.kind === "edge" || ctxMenu.target.kind === "trigger-edge"}
          Delete connection
        {:else if ctxMenu.target.kind === "trigger"}
          Delete trigger
        {:else}
          Delete node
        {/if}
      </span>
    </button>
  </div>
{/if}

<style>
  /* Mirror the legacy editor.css canvas bg — dot grid keyed to the
     theme. Light: white-200 plate with white-400 dots. Dark: navy-800
     plate with navy-600 dots. Grid is 18×18 to align with the row/col
     rhythm of the palette + inspector chrome on either side. */
  .wf-canvas-bg {
    background-color: #f1efeb;
    background-image: radial-gradient(circle, #d6cfc4 1px, transparent 1px);
    background-size: 18px 18px;
  }
  :global(.dark) .wf-canvas-bg {
    background-color: #131c2f;
    background-image: radial-gradient(circle, #2c3a5a 1px, transparent 1px);
  }
</style>
