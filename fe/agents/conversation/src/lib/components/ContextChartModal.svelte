<script lang="ts">
  /* The Context window chart, large. Same points, controls and selection
     as the panel, plus axes (turn and time below, tokens left, % or ≈$
     right) and an X zoom for a 500-point series: wheel or pinch zooms
     around the cursor, shift-drag pans, the overview strip underneath
     shows and moves the visible window, double-click resets. Esc or ✕
     closes it; the panel keeps the selection. */
  import type { Snippet } from "svelte";
  import {
    type Selection, type Seg, type View,
    brief, dayOf, niceTicks, paletteOf, peakOf, readout, runsOf, selectionSummary, usd,
  } from "../contextSeries.js";
  import { clock24 } from "@wick-fe/common-ui";

  type Props = {
    view: View;
    loading: boolean;
    sel: Selection | null;
    title: string;
    controls: Snippet;
    segs: Snippet<[Seg[]]>;
    legend?: Snippet;
    onClose: () => void;
    el?: HTMLDivElement;
  };
  let { view, loading, sel = $bindable(), title, controls, segs, legend, onClose, el = $bindable() }: Props = $props();

  const PAD = { l: 48, r: 56, t: 18, b: 34 };

  /* Element size, kept current with a ResizeObserver where there is one
     (a test DOM has none, and lays nothing out anyway). */
  function measure(node: HTMLElement, set: (w: number, h: number) => void) {
    set(node.clientWidth, node.clientHeight);
    if (typeof ResizeObserver === "undefined") return {};
    const ro = new ResizeObserver(() => set(node.clientWidth, node.clientHeight));
    ro.observe(node);
    return { destroy: () => ro.disconnect() };
  }
  const n = $derived(view.list.length);
  const validSel = $derived(sel && sel.b < n && sel.a !== sel.b ? sel : null);

  let W = $state(0);
  let H = $state(0);
  let OW = $state(0);
  const OH = 28;

  /* The visible X window in point indexes (fractional); null = all. */
  let z = $state<{ a: number; b: number } | null>(null);
  function clampWin(a: number, b: number): { a: number; b: number } {
    const minSpan = Math.min(9, n - 1);
    if (b - a < minSpan) {
      const c = (a + b) / 2;
      a = c - minSpan / 2;
      b = c + minSpan / 2;
    }
    if (b - a > n - 1) return { a: 0, b: n - 1 };
    if (a < 0) {
      b -= a;
      a = 0;
    }
    if (b > n - 1) {
      a -= b - (n - 1);
      b = n - 1;
    }
    return { a: Math.max(0, a), b };
  }
  const win = $derived(z ? clampWin(z.a, z.b) : { a: 0, b: Math.max(0, n - 1) });
  function setZ(a: number, b: number) {
    const w = clampWin(a, b);
    z = w.a <= 0.01 && w.b >= n - 1.01 ? null : w;
  }
  const resetZoom = () => (z = null);

  const pw = $derived(Math.max(1, W - PAD.l - PAD.r));
  const ph = $derived(Math.max(1, H - PAD.t - PAD.b));
  const i0 = $derived(Math.max(0, Math.floor(win.a)));
  const i1 = $derived(Math.min(n - 1, Math.ceil(win.b)));
  const X = (i: number) => PAD.l + ((i - win.a) / (win.b - win.a || 1)) * pw;
  const total = $derived(view.metric === "usage" && view.agg === "total");
  const yr = $derived.by(() => {
    let lo = Infinity;
    let hi = -Infinity;
    for (let i = i0; i <= i1; i++) {
      lo = Math.min(lo, view.vals[i]);
      hi = Math.max(hi, view.vals[i]);
    }
    const min = total ? lo : 0;
    return { min, max: hi > min ? hi + (hi - min) * 0.06 : min + 1 };
  });
  const Y = (v: number) => PAD.t + (1 - (v - yr.min) / (yr.max - yr.min)) * ph;
  const line = (from: number, to: number) => {
    const out: string[] = [];
    for (let i = from; i <= to; i++) out.push(`${X(i).toFixed(1)},${Y(view.vals[i]).toFixed(1)}`);
    return out.join(" ");
  };
  const runs = $derived(runsOf(view.list, i0, i1, view.merged));

  /* Right axis: % of the window, or dollars at the range's average rate. */
  const rate = $derived(
    view.list.reduce((s, q) => s + (q.cost ?? 0), 0) / Math.max(1, view.list.reduce((s, q) => s + (q.sent ?? 0), 0)),
  );
  const right = (v: number) =>
    view.metric === "usage"
      ? v * rate >= 100 ? `≈$${Math.round(v * rate)}` : `≈${usd(v * rate)}`
      : view.window > 0 ? `${Math.round((v / view.window) * 100)}%` : "";
  const yTicks = $derived(W > 0 ? niceTicks(yr.min, yr.max, 4) : []);

  /* X ticks: turn number, with its time under it (the date where the day changes). */
  const xTicks = $derived.by(() => {
    if (W === 0 || n < 2) return [];
    const nt = Math.max(2, Math.min(7, Math.floor(pw / 90)));
    const gap = pw / nt;
    let lastDay: string | null = null;
    const out: { x: number; turn: string; time: string; anchor: string }[] = [];
    for (let k = 0; k <= nt; k++) {
      const i = Math.round(win.a + ((win.b - win.a) * k) / nt);
      const q = view.list[i];
      if (!q) continue;
      let time = "";
      if (q.at !== null) {
        const day = dayOf(q.at);
        const hm = clock24(q.at);
        time = day !== lastDay ? (gap >= 80 ? `${day} ${hm}` : day) : hm;
        lastDay = day;
      }
      const turn = view.merged ? String(q.sturn ?? "—") : q.turn === null ? "—" : String(q.turn);
      out.push({ x: X(i), turn, time, anchor: k === 0 ? "start" : k === nt ? "end" : "middle" });
    }
    return out;
  });

  let hover = $state<number | null>(null);
  let hoverY = $state(0);
  const peak = $derived(validSel ? peakOf(view, validSel) : null);
  const summary = $derived(validSel ? selectionSummary(view, validSel) : []);
  const tipLines = $derived(hover !== null && hover >= i0 && hover <= i1 ? readout(view, hover) : null);
  let tipW = $state(0);
  let tipH = $state(0);
  const tipLeft = $derived(hover === null ? 0 : X(hover) + 14 + tipW > W ? Math.max(0, X(hover) - 14 - tipW) : X(hover) + 14);
  const tipTop = $derived(Math.min(Math.max(PAD.t, hoverY - tipH - 8), H - tipH));

  /* ---- pointer: hover, drag-select, shift-drag pan, pinch ---- */
  let bigbox: HTMLDivElement | undefined = $state();
  const ptrs = new Map<number, number>();
  type Mode =
    | { kind: "select"; start: number }
    | { kind: "pan"; x0: number; a0: number; b0: number }
    | { kind: "pinch"; d0: number; c: number; a0: number; b0: number; m0: number };
  let mode: Mode | null = null;
  const fIdx = (clientX: number) => {
    const rc = bigbox!.getBoundingClientRect();
    return win.a + ((clientX - rc.left - PAD.l) / pw) * (win.b - win.a);
  };
  const bIdx = (clientX: number) => Math.min(i1, Math.max(i0, Math.round(fIdx(clientX))));

  function onDown(e: PointerEvent) {
    if (loading || n < 2 || (e.target as Element).closest("[data-zoom-reset]")) return;
    ptrs.set(e.pointerId, e.clientX);
    bigbox?.setPointerCapture?.(e.pointerId);
    if (ptrs.size === 2) {
      const [x1, x2] = [...ptrs.values()];
      mode = { kind: "pinch", d0: Math.abs(x2 - x1) || 1, c: fIdx((x1 + x2) / 2), a0: win.a, b0: win.b, m0: (x1 + x2) / 2 };
    } else if (e.shiftKey || e.pointerType === "touch") {
      mode = { kind: "pan", x0: e.clientX, a0: win.a, b0: win.b };
    } else {
      mode = { kind: "select", start: bIdx(e.clientX) };
      sel = null;
    }
  }
  function onMove(e: PointerEvent) {
    if (loading || n < 2 || !bigbox) return;
    if (ptrs.has(e.pointerId)) ptrs.set(e.pointerId, e.clientX);
    hoverY = e.clientY - bigbox.getBoundingClientRect().top;
    hover = bIdx(e.clientX);
    if (mode?.kind === "pan") {
      const d = ((e.clientX - mode.x0) / pw) * (mode.b0 - mode.a0);
      setZ(mode.a0 - d, mode.b0 - d);
    } else if (mode?.kind === "pinch" && ptrs.size === 2) {
      const [x1, x2] = [...ptrs.values()];
      const f = mode.d0 / (Math.abs(x2 - x1) || 1);
      const shift = (((x1 + x2) / 2 - mode.m0) / pw) * (mode.b0 - mode.a0);
      setZ(mode.c - (mode.c - mode.a0) * f - shift, mode.c + (mode.b0 - mode.c) * f - shift);
    } else if (mode?.kind === "select" && hover !== mode.start) {
      sel = { a: Math.min(hover, mode.start), b: Math.max(hover, mode.start) };
    }
  }
  function onUp(e: PointerEvent) {
    ptrs.delete(e.pointerId);
    if (ptrs.size < 2 && mode?.kind === "pinch") mode = null;
    if (ptrs.size === 0) mode = null;
  }
  function onWheel(e: WheelEvent) {
    if (n < 2) return;
    e.preventDefault();
    const span = win.b - win.a;
    if (Math.abs(e.deltaX) > Math.abs(e.deltaY) || e.shiftKey) {
      const d = ((e.deltaX || e.deltaY) / pw) * span;
      setZ(win.a + d, win.b + d);
    } else {
      const f = Math.exp(e.deltaY * (e.ctrlKey ? 0.01 : 0.0015));
      const c = fIdx(e.clientX);
      setZ(c - (c - win.a) * f, c + (win.b - c) * f);
    }
  }
  $effect(() => {
    // passive: false, so the page does not scroll under a zoom
    const b = bigbox;
    if (!b) return;
    b.addEventListener("wheel", onWheel, { passive: false });
    return () => b.removeEventListener("wheel", onWheel);
  });

  /* ---- overview strip: drag the window to pan, its edges to resize, elsewhere to jump ---- */
  let ov: HTMLDivElement | undefined = $state();
  let omode: { kind: "left" } | { kind: "right" } | { kind: "move"; at0: number; a0: number; b0: number } | null = null;
  const OX = (i: number) => (n <= 1 ? 0 : (i / (n - 1)) * OW);
  const ovLine = $derived.by(() => {
    if (OW === 0 || n < 2) return "";
    const lo = total ? Math.min(...view.vals) : 0;
    const hi = Math.max(...view.vals, lo + 1);
    const step = Math.max(1, Math.floor(n / OW));
    const p: string[] = [];
    const y = (v: number) => OH - 2 - ((v - lo) / (hi - lo)) * (OH - 4);
    for (let i = 0; i < n; i += step) p.push(`${OX(i).toFixed(1)},${y(view.vals[i]).toFixed(1)}`);
    p.push(`${OW.toFixed(1)},${y(view.vals[n - 1]).toFixed(1)}`);
    return p.join(" ");
  });
  function ovAt(clientX: number) {
    const rc = ov!.getBoundingClientRect();
    return { at: ((clientX - rc.left) / rc.width) * (n - 1), x: clientX - rc.left };
  }
  function onOvDown(e: PointerEvent) {
    if (!ov || n < 2) return;
    const { at, x } = ovAt(e.clientX);
    const x1 = OX(win.a);
    const x2 = OX(win.b);
    ov.setPointerCapture?.(e.pointerId);
    if (Math.abs(x - x1) < 6) omode = { kind: "left" };
    else if (Math.abs(x - x2) < 6) omode = { kind: "right" };
    else if (x > x1 && x < x2) omode = { kind: "move", at0: at, a0: win.a, b0: win.b };
    else {
      const half = (win.b - win.a) / 2;
      setZ(at - half, at + half);
      omode = { kind: "move", at0: at, a0: at - half, b0: at + half };
    }
  }
  function onOvMove(e: PointerEvent) {
    if (!omode || !ov) return;
    const { at } = ovAt(e.clientX);
    if (omode.kind === "left") setZ(Math.min(at, win.b - 9), win.b);
    else if (omode.kind === "right") setZ(win.a, Math.max(at, win.a + 9));
    else {
      const d = at - omode.at0;
      setZ(omode.a0 + d, omode.b0 + d);
    }
  }

  $effect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      // Ours alone: the panel underneath must not close with it.
      e.preventDefault();
      e.stopImmediatePropagation();
      onClose();
    }
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });
  let closeBtn: HTMLButtonElement | undefined = $state();
  $effect(() => {
    closeBtn?.focus();
  });
</script>

<div bind:this={el} class="ctx-modal fixed inset-0 z-50 flex items-center justify-center" data-testid="context-modal">
  <div
    class="ctx-mbox flex flex-col rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-lg"
    role="dialog"
    aria-modal="true"
    aria-label="Chart"
  >
    <div class="px-4 py-2.5 flex items-center justify-between border-b border-white-300 dark:border-navy-600">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">{title}</h2>
      <button
        bind:this={closeBtn}
        type="button"
        aria-label="Close"
        class="rounded-lg px-2 py-1 text-xs text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
        onclick={onClose}>✕</button
      >
    </div>
    <div class="ctx-mbody flex flex-col px-4 py-3">
      {@render controls()}
      <div
        bind:this={bigbox}
        use:measure={(w, h) => {
          W = w;
          H = h;
        }}
        class="ctx-big relative mt-2 w-full text-black-700 dark:text-black-600"
        class:loading
        role="presentation"
        onpointerdown={onDown}
        onpointermove={onMove}
        onpointerup={onUp}
        onpointercancel={onUp}
        onpointerleave={() => {
          if (!mode) hover = null;
        }}
        ondblclick={(e) => {
          if (!(e.target as Element).closest("[data-zoom-reset]")) resetZoom();
        }}
      >
        {#if W > 0 && H > 0 && n >= 2}
          <svg class="absolute inset-0 h-full w-full cursor-crosshair" viewBox="0 0 {W} {H}" role="img" aria-label="Chart">
            <defs><clipPath id="ctx-plot"><rect x={PAD.l} y="0" width={pw} height={H} /></clipPath></defs>
            {#each yTicks as v (v)}
              <line x1={PAD.l} x2={W - PAD.r} y1={Y(v)} y2={Y(v)} stroke="currentColor" opacity="0.12" />
              <text x={PAD.l - 6} y={Y(v)} dy="3" text-anchor="end" font-size="10" fill="currentColor">{brief(v)}</text>
              <text x={W - PAD.r + 6} y={Y(v)} dy="3" font-size="10" fill="currentColor">{right(v)}</text>
            {/each}
            <text x={PAD.l - 6} y={PAD.t - 10} text-anchor="end" font-size="10" fill="currentColor" opacity="0.7">tokens</text>
            <text x={W - PAD.r + 6} y={PAD.t - 10} font-size="10" fill="currentColor" opacity="0.7">{view.metric === "usage" ? "$" : "%"}</text>
            {#each xTicks as t, k (k)}
              <line x1={t.x} x2={t.x} y1={PAD.t} y2={H - PAD.b} stroke="currentColor" opacity="0.06" />
              <text x={t.x} y={H - PAD.b + 13} text-anchor={t.anchor} font-size="10" fill="currentColor">{t.turn}</text>
              <text x={t.x} y={H - PAD.b + 26} text-anchor={t.anchor} font-size="9.5" fill="currentColor" opacity="0.6">{t.time}</text>
            {/each}
            <text x={PAD.l - 16} y={H - PAD.b + 13} text-anchor="end" font-size="10" fill="currentColor" opacity="0.7">turn</text>
            <g clip-path="url(#ctx-plot)">
              {#if validSel}
                <rect x={X(validSel.a)} y={PAD.t} width={Math.max(1, X(validSel.b) - X(validSel.a))} height={ph} fill="currentColor" opacity="0.12" />
              {/if}
              {#each runs as r (r.from)}
                <g class={r.color >= 0 ? paletteOf(r.color).text : ""}>
                  <polygon points="{X(r.from).toFixed(1)},{PAD.t + ph} {line(r.from, r.to)} {X(r.to).toFixed(1)},{PAD.t + ph}" fill="currentColor" opacity={r.color >= 0 ? 0.18 : 0.12} />
                  <polyline points={line(r.from, r.to)} fill="none" stroke="currentColor" stroke-width={r.color >= 0 ? 1.75 : 1.5} vector-effect="non-scaling-stroke" />
                </g>
              {/each}
              {#if peak !== null}
                <circle class="text-black-900 dark:text-white-100" cx={X(peak)} cy={Y(view.vals[peak])} r="4" fill="currentColor" />
              {/if}
              {#if hover !== null && hover >= i0 && hover <= i1}
                <line x1={X(hover)} x2={X(hover)} y1={PAD.t} y2={H - PAD.b} stroke="currentColor" opacity="0.5" />
                <circle
                  class={view.merged ? paletteOf(view.list[hover].color).text : ""}
                  cx={X(hover)}
                  cy={Y(view.vals[hover])}
                  r="3.5"
                  fill="currentColor"
                  opacity={view.list[hover].turn === null ? 0.45 : 1}
                />
              {/if}
            </g>
          </svg>
        {/if}
        <div class="shim pointer-events-none text-link-400" aria-hidden="true"></div>
        {#if z}
          <button
            type="button"
            data-zoom-reset
            class="ctx-zreset absolute rounded-md border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-900 dark:text-white-100 shadow-sm hover:bg-white-200 dark:hover:bg-navy-800"
            style="top: {PAD.t + 6}px; right: {PAD.r + 6}px"
            onclick={resetZoom}>Reset zoom</button
          >
        {/if}
        {#if tipLines}
          <div
            use:measure={(w, h) => {
              tipW = w;
              tipH = h;
            }}
            class="pointer-events-none absolute whitespace-nowrap rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 px-2 py-1 text-[11px] text-black-700 dark:text-black-600 shadow-lg dark:bg-navy-800 tabular-nums"
            style="left: {tipLeft}px; top: {tipTop}px"
          >
            {@render segs(tipLines[0])}
            {#if tipLines[1].length}<span class="block">{@render segs(tipLines[1])}</span>{/if}
          </div>
        {/if}
      </div>
      <div class="ctx-msum mt-1 flex items-center gap-2 text-[11px] text-black-700 dark:text-black-600 tabular-nums">
        {#if validSel}
          <span class="min-w-0">{@render segs(summary)}</span>
          <button
            type="button"
            class="rounded-md border border-white-300 dark:border-navy-600 px-2 py-0.5 font-medium text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800"
            onclick={() => validSel && setZ(validSel.a, validSel.b)}>Zoom to selection</button
          >
          <button
            type="button"
            aria-label="Clear selection"
            title="Clear selection (Esc)"
            class="ml-1 rounded px-1 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
            onclick={() => (sel = null)}>×</button
          >
        {:else}
          <span class="opacity-70">Drag to select · wheel to zoom · shift+drag to pan · double-click to reset</span>
        {/if}
        {#if legend}<span class="ml-auto flex items-center gap-3">{@render legend()}</span>{/if}
      </div>
      <div
        bind:this={ov}
        use:measure={(w) => (OW = w)}
        class="ctx-ov relative mt-1 w-full text-black-700 dark:text-black-600"
        role="presentation"
        onpointerdown={onOvDown}
        onpointermove={onOvMove}
        onpointerup={() => (omode = null)}
      >
        {#if ovLine}
          <svg class="absolute inset-0 h-full w-full" viewBox="0 0 {OW} {OH}" aria-label="Overview">
            <rect x="0" y="0" width={OW} height={OH} rx="4" fill="currentColor" opacity="0.05" />
            <polygon points="0,{OH} {ovLine} {OW},{OH}" fill="currentColor" opacity="0.12" />
            <polyline points={ovLine} fill="none" stroke="currentColor" stroke-width="1" />
            <rect x={OX(win.a)} y="0.5" width={Math.max(2, OX(win.b) - OX(win.a))} height={OH - 1} rx="3" fill="currentColor" opacity="0.16" stroke="currentColor" stroke-opacity="0.6" />
            <rect x={OX(win.a) - 1.5} y={OH / 2 - 6} width="3" height="12" rx="1" fill="currentColor" />
            <rect x={OX(win.b) - 1.5} y={OH / 2 - 6} width="3" height="12" rx="1" fill="currentColor" />
          </svg>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .ctx-modal {
    background: rgba(2, 6, 23, 0.55);
  }
  .ctx-mbox {
    width: 90vw;
    height: 70vh;
  }
  .ctx-mbody {
    flex: 1 1 auto;
    min-height: 0;
  }
  .ctx-big {
    flex: 1 1 auto;
    min-height: 0;
    touch-action: none;
    user-select: none;
  }
  .ctx-msum {
    min-height: 1.5rem;
  }
  .ctx-ov {
    height: 28px;
    touch-action: none;
    user-select: none;
    cursor: grab;
  }
  .ctx-zreset {
    background: rgba(255, 255, 255, 0.85);
    backdrop-filter: blur(2px);
  }
  :global(.dark) .ctx-zreset {
    background: rgba(15, 23, 42, 0.8);
  }
  .ctx-big .shim {
    position: absolute;
    left: 0;
    right: 0;
    top: 0;
    height: 2px;
    overflow: hidden;
    display: none;
    border-radius: 2px;
  }
  .ctx-big .shim::after {
    content: "";
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    width: 40%;
    background: linear-gradient(90deg, transparent, currentColor, transparent);
    animation: ctx-mshim 1s linear infinite;
  }
  .ctx-big.loading .shim {
    display: block;
  }
  .ctx-big.loading svg {
    filter: blur(2px);
    opacity: 0.55;
  }
  @keyframes ctx-mshim {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(250%);
    }
  }
</style>
