/* The blob-mascot kind of agent avatar: the Svelte side of the vendored
   blobmascot core (./blob/, MIT, see ./blob/README.md). Pure helpers live
   here so BlobAvatar.svelte stays a thin renderer and the rules below are
   testable without a canvas.

   Cost model (PLAN 6.5): only an avatar marked `live` (chat header, empty
   state, settings preview) gets an animated canvas, and those ride the
   shared ticker loop while on screen. Every other size — roster rows,
   mentions, pickers — is a still PNG drawn once and cached by look, so a
   long list never owns a canvas or a loop. */
import { createRuntime } from "./blob/core/runtime";
import { drawFrame } from "./blob/render/canvas";
import { EXPRESSIONS, PALETTE, SHAPES, type BlobExpression, type BlobShape, type BlobState, type Gaze } from "./blob/core/types";
import { normalizeShape, type AvatarState } from "./shape.js";

export type { BlobExpression, BlobShape, BlobState, Gaze };

/** Avatar.kind on the server: "" (or absent) is the classic avatar. */
export const AVATAR_KIND_BLOB = "blob";
export type AvatarKind = "" | typeof AVATAR_KIND_BLOB;

export const isBlobKind = (kind: string | null | undefined) => kind === AVATAR_KIND_BLOB;

export const BLOB_SHAPES: readonly BlobShape[] = SHAPES;
export const BLOB_EXPRESSIONS: readonly BlobExpression[] = EXPRESSIONS;
/** The blob palette, lowercased so it compares with <input type="color">. */
export const BLOB_COLORS: string[] = PALETTE.map((c) => c.toLowerCase());

/** Below this size the eyes are a few pixels wide: following the pointer
    there is noise, so only big avatars look around. */
export const BLOB_GAZE_MIN = 64;

export function normalizeBlobShape(s: string | null | undefined): BlobShape {
  return BLOB_SHAPES.includes(s as BlobShape) ? (s as BlobShape) : "circle";
}

export function normalizeBlobExpression(e: string | null | undefined): BlobExpression {
  return BLOB_EXPRESSIONS.includes(e as BlobExpression) ? (e as BlobExpression) : "neutral";
}

/** blobStateFor maps the roster's avatar state onto a blob motion. A
    hatching egg has no blob counterpart; the component's pop-in CSS does
    that job, so it breathes as idle. */
export function blobStateFor(s: AvatarState): BlobState {
  switch (s) {
    case "thinking":
    case "orbit":
    case "alert":
    case "notify":
    case "sleep":
      return s;
    default:
      return "idle";
  }
}

/** blobAnimates: an animated canvas only where asked for (live), never
    under prefers-reduced-motion or `still`. */
export function blobAnimates(o: { live?: boolean; still?: boolean; reduced?: boolean }): boolean {
  return !!o.live && !o.still && !o.reduced;
}

/** blobFollowsGaze: big, animated and awake. */
export function blobFollowsGaze(size: number, state: BlobState): boolean {
  return size >= BLOB_GAZE_MIN && state !== "sleep";
}

/** gazeFromOffset turns the pointer's offset from the avatar centre (px)
    into yaw/pitch degrees, the same mapping as the package's React
    pointer hook (react/pointer.ts, not vendored). */
export function gazeFromOffset(dx: number, dy: number, vw: number, vh: number): Gaze {
  const c = (v: number) => Math.min(1, Math.max(-1, v));
  return { yaw: c(dx / (vw * 0.32)) * 36, pitch: c(dy / (vh * 0.32)) * -28 };
}

export type BlobLook = { shape: BlobShape; expression: BlobExpression; color: string };

/** randomBlob is the picker's "shuffle". rand is injectable for tests. */
export function randomBlob(rand: () => number = Math.random): BlobLook {
  const pick = <T>(xs: readonly T[]) => xs[Math.min(xs.length - 1, Math.floor(rand() * xs.length))];
  return { shape: pick(BLOB_SHAPES), expression: pick(BLOB_EXPRESSIONS), color: pick(BLOB_COLORS) };
}

/** blobColor keeps a stored color the canvas can parse (#rgb / #rrggbb);
    anything else gets the first non-black swatch rather than the core's
    silent blue fallback. */
export function blobColor(c: string | null | undefined): string {
  const v = (c ?? "").trim();
  return /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.test(v) ? v.toLowerCase() : BLOB_COLORS[8];
}

export type AvatarSpec = { kind?: string; shape: string; color: string; expression?: string; events?: Record<string, { state?: string; expression?: string; off?: boolean }> };

/** switchAvatarKind converts a stored avatar between classic and blob,
    keeping the color and any shape both kinds share (circle, squircle,
    triangle). A classic spec carries no kind/expression keys at all, so it
    compares and encodes exactly like a row written before blobs. The
    event table (events.ts) belongs to the agent, not the look: it stays. */
export function switchAvatarKind(a: AvatarSpec, kind: string): AvatarSpec {
  const ev = a.events ? { events: a.events } : {};
  if (isBlobKind(kind)) return { kind: AVATAR_KIND_BLOB, shape: normalizeBlobShape(a.shape), color: a.color, expression: normalizeBlobExpression(a.expression), ...ev };
  return { shape: normalizeShape(a.shape), color: a.color, ...ev };
}

export function snapshotOf(look: BlobLook, state: BlobState = "idle", gaze: Gaze = { yaw: 0, pitch: 0 }) {
  return { shape: look.shape, expression: look.expression, color: look.color, state, gaze };
}

/** stillKey identifies one cached still frame. */
export const stillKey = (look: BlobLook, state: BlobState, px: number) =>
  `${look.shape}|${look.expression}|${look.color}|${state}|${px}`;

const STILL_MAX = 256;
const stills = new Map<string, string>();

/** stillUrl draws one frame (t = 0, no blink) at px device pixels and
    returns it as a data URL, cached by look + state + px. The cache is a
    small LRU: a roster of fifty agents redraws nothing on re-render. ""
    when there is no canvas 2D (SSR, jsdom); the caller shows a plain dot. */
export function stillUrl(look: BlobLook, state: BlobState, px: number): string {
  const key = stillKey(look, state, px);
  const hit = stills.get(key);
  if (hit !== undefined) {
    stills.delete(key);
    stills.set(key, hit);
    return hit;
  }
  let url = "";
  if (typeof document !== "undefined") {
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = px;
    let ctx: CanvasRenderingContext2D | null = null;
    try {
      ctx = canvas.getContext("2d");
    } catch {
      ctx = null;
    }
    if (ctx) {
      const snap = snapshotOf(look, state);
      drawFrame(ctx, createRuntime(snap).step(snap, 1, 0), px, 0);
      try {
        url = canvas.toDataURL("image/png");
      } catch {
        url = "";
      }
    }
  }
  if (!url) return "";
  stills.set(key, url);
  if (stills.size > STILL_MAX) stills.delete(stills.keys().next().value as string);
  return url;
}

/** Test hook: how many stills are cached, and a way to empty the cache. */
export const stillCacheSize = () => stills.size;
export const clearStillCache = () => stills.clear();

export { createRuntime, drawFrame };
