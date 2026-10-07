// Geometry helpers for sticky_note boards. A board's `content` is its
// title; its `texts` are small sticky cards stored relative (0..1) to the
// board so a resize moves every card proportionally. The canvas converts
// to px only to render and to apply drag deltas.
import type { StickyNoteColor, StickyText, StickyTextSize } from "$lib/types/workflow";

export const STICKY_COLORS: StickyNoteColor[] = ["yellow", "green", "blue", "purple", "red", "gray"];
export const STICKY_TEXT_SIZES: StickyTextSize[] = ["sm", "md", "lg"];
// Card width when unset / created by double-click (relative).
export const STICKY_TEXT_W = 0.4;
// Smallest board a resize handle can produce (canvas px).
export const STICKY_MIN_W = 160;
export const STICKY_MIN_H = 100;

export function clamp01(v: number): number {
  return Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0;
}

// Keep a card inside its board: width in (minW..1], x so the card's
// right edge stays inside, y so its top (and its height `h`, relative,
// when known) stays inside.
export function clampText(t: StickyText, h = 0, minW = 0.05): StickyText {
  const width = Math.min(1, Math.max(minW, Number.isFinite(t.width) && t.width! > 0 ? t.width! : STICKY_TEXT_W));
  const x = Math.min(clamp01(t.x), 1 - width);
  const y = Math.min(clamp01(t.y), Math.max(0, 1 - clamp01(h)));
  return { ...t, x, y, width };
}

export function toRelative(px: number, size: number): number {
  return size > 0 ? px / size : 0;
}

export function toPx(rel: number, size: number): number {
  return rel * size;
}

export function newTextId(texts: StickyText[]): string {
  const used = new Set(texts.map((t) => t.id));
  let i = texts.length + 1;
  while (used.has(`t${i}`)) i++;
  return `t${i}`;
}

// Resize handle directions, image-editor style: 4 corners + 4 edges.
export type ResizeDir = "n" | "ne" | "e" | "se" | "s" | "sw" | "w" | "nw";
export const RESIZE_DIRS: ResizeDir[] = ["nw", "n", "ne", "e", "se", "s", "sw", "w"];
export type Rect = { x: number; y: number; w: number; h: number };

// Apply a handle drag (dx, dy in canvas px) to the board rect. Dragging
// a left/top handle moves x/y so the opposite edge stays put; the size
// never goes below min.
export function resizeRect(dir: ResizeDir, r: Rect, dx: number, dy: number, minW = STICKY_MIN_W, minH = STICKY_MIN_H): Rect {
  let { x, y, w, h } = r;
  if (dir.includes("e")) w = Math.max(minW, r.w + dx);
  if (dir.includes("w")) {
    w = Math.max(minW, r.w - dx);
    x = r.x + r.w - w;
  }
  if (dir.includes("s")) h = Math.max(minH, r.h + dy);
  if (dir.includes("n")) {
    h = Math.max(minH, r.h - dy);
    y = r.y + r.h - h;
  }
  return { x: Math.round(x), y: Math.round(y), w: Math.round(w), h: Math.round(h) };
}
