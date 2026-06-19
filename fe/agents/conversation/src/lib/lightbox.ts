/* Fullscreen zoom/pan viewer for rendered diagrams (mermaid / svg). Opened by
   double-clicking a rendered block. The overlay is appended to <body> with an
   OPAQUE backdrop so messages below are fully covered (no bleed-through).

   The backdrop colour is switchable: a diagram authored for a dark canvas can
   be invisible on a dark overlay (and vice-versa), so the user cycles the
   backdrop (auto-theme → light → dark → checkerboard) until the diagram reads
   clearly. The choice persists across opens via localStorage.

   Interactions (mirrors the workflow canvas convention):
   - plain wheel / two-finger trackpad scroll → pan
   - ⌘/Ctrl + wheel, or pinch (browser sends ctrl+wheel) → zoom toward cursor
   - drag (mouse / one-finger / pen) → pan
   - double-click inside → reset to fit
   - Esc / close button / click on bare backdrop → dismiss

   Gestures mutate a `target` transform; a rAF loop eases `cur → target` for
   reset (so it glides), while live wheel/drag/pinch write through instantly for
   1:1 tracking. The svg uses a plain 2D transform (not translate3d) so it stays
   crisp vector at any zoom instead of being bitmap-scaled on a GPU layer. */

import { correctViewBox } from "./richRender.js";

let overlay: HTMLElement | null = null;
let teardown: (() => void) | null = null;

/* surface colours straight from the app palette (white-100 / navy-800) as a
   fallback only. Applied as an inline background — NOT a Tailwind `dark:` class
   — so it resolves regardless of where the overlay sits or how purge treated
   the class. */
const LIGHT_BG = "rgb(var(--color-white-100))";
const DARK_BG = "rgb(var(--color-navy-800))";

/* "Auto" reads the ACTUAL rendered background of the chat surface the diagram
   sits in (walk up to the first ancestor with a non-transparent background),
   so it matches whatever theme is active — not just a light/dark guess. The
   source element is captured when the lightbox opens. */
let autoBg = "";
function computeAutoBg(from: Element | null): string {
  let el: Element | null = from;
  while (el && el !== document.documentElement) {
    const bg = getComputedStyle(el).backgroundColor;
    /* skip transparent / fully see-through layers */
    if (bg && bg !== "transparent" && !/rgba?\([^)]*,\s*0\s*\)$/.test(bg)) return bg;
    el = el.parentElement;
  }
  /* nothing opaque found → fall back to the palette light/dark guess */
  const dark = document.documentElement.classList.contains("dark");
  return dark ? DARK_BG : LIGHT_BG;
}

/* backdrop modes — index into BACKDROPS, persisted. Grid uses a CSS class
   instead of an inline bg → returns "". */
const BACKDROPS = [
  { key: "auto", label: "Auto", grid: false, bg: () => autoBg || LIGHT_BG },
  { key: "light", label: "Light", grid: false, bg: () => LIGHT_BG },
  { key: "dark", label: "Dark", grid: false, bg: () => DARK_BG },
  { key: "grid", label: "Grid", grid: true, bg: () => "" },
] as const;
const BACKDROP_BASE = "fixed inset-0 z-[100] select-none touch-none";

/* apply a backdrop mode to the overlay root (inline bg for solid modes, the
   .wick-lb-grid class for the checkerboard). */
function applyBackdrop(root: HTMLElement, i: number): void {
  const mode = BACKDROPS[i];
  root.classList.toggle("wick-lb-grid", mode.grid);
  root.style.background = mode.bg();
}

function dismiss(): void {
  if (!overlay) return;
  overlay.remove();
  overlay = null;
  document.removeEventListener("keydown", onKey);
  teardown?.();
  teardown = null;
}

function onKey(e: KeyboardEvent): void {
  if (e.key === "Escape") dismiss();
}

/* Opens the lightbox showing a clone of `svg`. `sourceEl` (the chat block the
   diagram lives in) is sampled for the "Auto" backdrop colour. */
export function openLightbox(svg: SVGSVGElement, sourceEl?: Element | null): void {
  dismiss();

  autoBg = computeAutoBg(sourceEl ?? svg.parentElement);
  /* always open at "Auto" (index 0) — the backdrop should match the current
     chat surface on every open, not remember a prior manual override */
  let bgIndex = 0;

  const root = document.createElement("div");
  root.className = BACKDROP_BASE;
  applyBackdrop(root, bgIndex);

  /* stage does NOT center the clone — the clone sits at its top-left (0,0) and
     ALL positioning is done via transform with origin 0 0. Centering it with
     flexbox would desync the zoom-pivot math (which assumes origin 0 0 at the
     stage's top-left). */
  const stage = document.createElement("div");
  stage.className = "absolute inset-0 overflow-hidden cursor-grab";
  root.appendChild(stage);

  const clone = svg.cloneNode(true) as SVGSVGElement;
  /* No max-width/height caps here: fit() computes the scale that makes the
     diagram fit the stage. A CSS cap would shrink the layout box first, then
     fit() would shrink AGAIN (double-shrink → tiny). Mermaid stamps an inline
     `max-width:Npx` on its <svg>; clear it so the natural size is measurable. */
  clone.style.setProperty("max-width", "none", "important");
  clone.style.setProperty("max-height", "none", "important");
  clone.style.width = "auto";
  clone.style.height = "auto";
  clone.style.position = "absolute";
  clone.style.left = "0";
  clone.style.top = "0";
  clone.style.transformOrigin = "0 0";
  stage.appendChild(clone);

  /* controls cluster (top-right) */
  const controls = document.createElement("div");
  controls.className = "absolute top-3 right-3 z-10 flex items-center gap-2";

  /* live zoom-level readout (e.g. "150%") */
  const zoomLabel = document.createElement("div");
  zoomLabel.className =
    "inline-flex items-center justify-center h-9 min-w-[3.5rem] px-3 rounded-full bg-white-200/90 dark:bg-navy-700/90 text-black-700 dark:text-black-500 text-xs font-medium shadow-md backdrop-blur tabular-nums";
  zoomLabel.textContent = "100%";

  const ctlBtn = (aria: string, inner: string): HTMLButtonElement => {
    const b = document.createElement("button");
    b.type = "button";
    b.setAttribute("aria-label", aria);
    b.className =
      "inline-flex items-center justify-center h-9 rounded-full bg-white-200/90 dark:bg-navy-700/90 text-black-700 dark:text-black-500 hover:text-black-900 dark:hover:text-white-100 shadow-md backdrop-blur transition-colors";
    b.innerHTML = inner;
    return b;
  };

  const bgBtn = ctlBtn("Change background", "");
  bgBtn.classList.add("px-3", "gap-1.5", "text-xs");
  function paintBgBtn(): void {
    bgBtn.innerHTML = `<svg viewBox="0 0 20 20" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="10" cy="10" r="7"/><path d="M10 3v14M3 10h14" opacity=".5"/></svg><span>${BACKDROPS[bgIndex].label}</span>`;
  }
  paintBgBtn();
  bgBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    bgIndex = (bgIndex + 1) % BACKDROPS.length;
    applyBackdrop(root, bgIndex);
    paintBgBtn();
  });

  const resetBtn = ctlBtn(
    "Reset view",
    `<svg viewBox="0 0 20 20" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M4 10a6 6 0 1 1 1.8 4.3"/><path d="M4 14v-3.5H7.5"/></svg>`,
  );
  resetBtn.classList.add("w-9");
  resetBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    reset();
  });

  const close = ctlBtn(
    "Close",
    `<svg viewBox="0 0 20 20" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M5 5l10 10M15 5L5 15"/></svg>`,
  );
  close.classList.add("w-9");
  close.addEventListener("click", (e) => {
    e.stopPropagation();
    dismiss();
  });

  controls.append(zoomLabel, bgBtn, resetBtn, close);

  const hint = document.createElement("div");
  hint.className =
    "absolute bottom-3 left-1/2 -translate-x-1/2 z-10 text-[11px] text-black-600 dark:text-black-600 bg-white-200/80 dark:bg-navy-700/80 rounded-full px-3 py-1 shadow-sm backdrop-blur pointer-events-none";
  hint.textContent =
    "scroll to pan · ⌘/Ctrl + scroll or pinch to zoom · drag to pan · double-click to reset · Esc to close";

  root.append(controls, hint);
  overlay = root;
  document.body.appendChild(root);
  document.addEventListener("keydown", onKey);

  /* ---- transform state: `target` is set by gestures, `cur` is what's drawn.
     A rAF loop eases cur → target so zoom/pan glide instead of stepping. ---- */
  const cur = { scale: 1, x: 0, y: 0 };
  const target = { scale: 1, x: 0, y: 0 };
  let raf = 0;

  function scheduleFrame(): void {
    if (raf) return;
    raf = requestAnimationFrame(frame);
  }

  function frame(): void {
    raf = 0;
    /* exponential ease toward target; snap when close to avoid endless rAF */
    const k = 0.28;
    cur.scale += (target.scale - cur.scale) * k;
    cur.x += (target.x - cur.x) * k;
    cur.y += (target.y - cur.y) * k;
    const near =
      Math.abs(target.scale - cur.scale) < 0.001 &&
      Math.abs(target.x - cur.x) < 0.1 &&
      Math.abs(target.y - cur.y) < 0.1;
    if (near) {
      cur.scale = target.scale;
      cur.x = target.x;
      cur.y = target.y;
    }
    draw();
    if (!near) scheduleFrame();
  }

  function draw(): void {
    /* 2D translate+scale (NOT translate3d): translate3d promotes the SVG to a
       GPU layer the browser snapshots once and bitmap-scales → blurry on zoom.
       A plain 2D transform repaints the vector crisply at every scale. */
    clone.style.transform = `translate(${cur.x}px, ${cur.y}px) scale(${cur.scale})`;
    zoomLabel.textContent = `${Math.round(cur.scale * 100)}%`;
  }

  function commit(): void {
    scheduleFrame();
  }

  /* During a live drag/pinch we want 1:1 tracking (no easing lag), so write
     cur = target directly and paint immediately. */
  function commitInstant(): void {
    cur.scale = target.scale;
    cur.x = target.x;
    cur.y = target.y;
    draw();
  }

  let stageRect = stage.getBoundingClientRect();
  function refreshRect(): void {
    stageRect = stage.getBoundingClientRect();
  }
  window.addEventListener("resize", refreshRect);

  /* zoom keeping the point under (clientX,clientY) fixed. Operates on TARGET. */
  function zoomAt(clientX: number, clientY: number, factor: number, instant: boolean): void {
    const px = clientX - stageRect.left;
    const py = clientY - stageRect.top;
    const next = Math.min(6, Math.max(0.4, target.scale * factor));
    const ratio = next / target.scale;
    target.x = px - (px - target.x) * ratio;
    target.y = py - (py - target.y) * ratio;
    target.scale = next;
    if (instant) commitInstant();
    else commit();
  }

  /* Fit the whole diagram inside the stage and centre it. The clone's
     UNTRANSFORMED size (natural layout box) is recovered by dividing the live
     bbox by the current scale. We then pick a scale that makes that box fit the
     stage with a small margin (never upscaling past 1), and offset so it's
     centred. transform-origin is 0 0, so x/y are the box's top-left. */
  function fit(instant: boolean): void {
    refreshRect();
    const box = clone.getBoundingClientRect();
    const s = target.scale || 1;
    const natW = box.width / s;
    const natH = box.height / s;
    if (!natW || !natH) return;
    const margin = 0.94; /* leave a little breathing room around the edges */
    const fitScale = Math.min((stageRect.width * margin) / natW, (stageRect.height * margin) / natH, 1);
    target.scale = fitScale;
    target.x = (stageRect.width - natW * fitScale) / 2;
    target.y = (stageRect.height - natH * fitScale) / 2;
    if (instant) commitInstant();
    else commit();
  }

  function reset(): void {
    fit(false);
  }

  stage.addEventListener(
    "wheel",
    (e) => {
      e.preventDefault();
      refreshRect();
      /* Canvas convention (mirrors the workflow editor): plain wheel / two-
         finger trackpad scroll → PAN; ⌘/Ctrl + wheel → ZOOM anchored at the
         cursor. Pinch arrives as ctrl+wheel from the browser, so it zooms too. */
      if (e.ctrlKey || e.metaKey) {
        const step = Math.exp(-e.deltaY * 0.005);
        zoomAt(e.clientX, e.clientY, step, true);
      } else {
        target.x -= e.deltaX;
        target.y -= e.deltaY;
        commitInstant();
      }
    },
    { passive: false },
  );

  stage.addEventListener("dblclick", (e) => {
    e.preventDefault();
    reset();
  });

  /* pointer-based pan + pinch (mouse / touch / pen) */
  const pointers = new Map<number, { x: number; y: number }>();
  let pinchDist = 0;

  const twoPts = () => [...pointers.values()];
  const distOf = () => {
    const p = twoPts();
    return Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y);
  };
  const midOf = () => {
    const p = twoPts();
    return { x: (p[0].x + p[1].x) / 2, y: (p[0].y + p[1].y) / 2 };
  };

  stage.addEventListener("pointerdown", (e) => {
    if (e.target === stage) {
      /* bare backdrop tap closes (only for the first pointer) */
      if (pointers.size === 0) dismiss();
      return;
    }
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    stage.setPointerCapture(e.pointerId);
    stage.style.cursor = "grabbing";
    refreshRect();
    if (pointers.size === 2) pinchDist = distOf();
  });

  stage.addEventListener("pointermove", (e) => {
    const prev = pointers.get(e.pointerId);
    if (!prev) return;
    const dx = e.clientX - prev.x;
    const dy = e.clientY - prev.y;
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });

    if (pointers.size === 2) {
      const dist = distOf();
      if (pinchDist > 0) {
        const mid = midOf();
        zoomAt(mid.x, mid.y, dist / pinchDist, true);
      }
      pinchDist = dist;
      return;
    }
    /* single-pointer drag = pan, tracked 1:1 (instant) */
    target.x += dx;
    target.y += dy;
    commitInstant();
  });

  function releasePointer(e: PointerEvent): void {
    pointers.delete(e.pointerId);
    if (pointers.size < 2) pinchDist = 0;
    if (pointers.size === 0) stage.style.cursor = "grab";
  }
  stage.addEventListener("pointerup", releasePointer);
  stage.addEventListener("pointercancel", releasePointer);

  /* initial fit once layout has settled (clone needs a frame to get its size).
     correctViewBox first, in case this svg came from a path that didn't run it
     (the chat renderer already fixes inline blocks; this covers the rest). */
  requestAnimationFrame(() => {
    correctViewBox(clone);
    fit(true);
  });

  /* dismiss() runs this to release the per-instance listeners + rAF */
  teardown = () => {
    window.removeEventListener("resize", refreshRect);
    if (raf) cancelAnimationFrame(raf);
  };
}

/* Wires double-click on a rendered block to open its <svg> in the lightbox.
   Idempotent via a data marker. `getSvg` returns the live rendered svg. */
export function enableZoom(block: HTMLElement, getSvg: () => SVGSVGElement | null): void {
  if (block.hasAttribute("data-zoomable")) return;
  block.setAttribute("data-zoomable", "");
  block.style.cursor = "zoom-in";
  block.addEventListener("dblclick", (e) => {
    const svg = getSvg();
    if (!svg) return;
    e.preventDefault();
    /* sample the bubble/surface bg (parent of the diagram block) for "Auto" */
    openLightbox(svg, block.parentElement);
  });
}
