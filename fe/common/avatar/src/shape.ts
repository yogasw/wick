/* Geometry for AgentAvatar's blob. Pure functions of (shape, state, time,
   gaze) so the component stays a thin renderer and every frame can be
   tested without a DOM. This is a light imitation of the bloub engine
   (PLAN 6.5), not a port of it: a superformula outline that breathes,
   two dark eyes that blink and follow the pointer, and a few states. No
   bloub code is copied here. The "blob" avatar kind is a separate,
   vendored engine with its own MIT notice: see blob.ts and blob/.

   Coordinates are unit space: the outline's widest point sits at radius
   BODY_R around (0, 0), drawn in a viewBox of -1.4..1.4 so a wobble or a
   hover scale never clips. */

export type AvatarShape = "circle" | "squircle" | "triangle" | "diamond";
export const AVATAR_SHAPES: AvatarShape[] = ["circle", "squircle", "triangle", "diamond"];

/* Palette offered in Settings → Avatar. Free hex is still accepted from the
   server; these are just the swatches. */
export const AVATAR_COLORS = [
  "#6366f1", "#27b199", "#0ea5e9", "#f59e0b", "#ef4444", "#ec4899", "#8b5cf6", "#64748b",
];

/** Phase 1a states (PLAN 6.5). orbit = the turn is on a tool, sleep =
    disabled agent, egg = just created. */
export type AvatarState = "idle" | "thinking" | "orbit" | "alert" | "notify" | "sleep" | "egg";
export const AVATAR_STATES: AvatarState[] = ["idle", "thinking", "orbit", "alert", "notify", "sleep", "egg"];

/** Captions for the state grid in Settings → Avatar. */
export const AVATAR_STATE_LABELS: Record<AvatarState, string> = {
  idle: "idle",
  thinking: "working",
  orbit: "using a tool",
  alert: "needs attention",
  notify: "new message",
  sleep: "disabled",
  egg: "hatching",
};

/** hashHandle is 32-bit FNV-1a over the handle's UTF-16 code units. The
    server's team.DefaultAvatarFor runs the same hash over the same
    (ASCII-only) handles, so both sides pick the same default. */
export function hashHandle(handle: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < handle.length; i++) {
    h ^= handle.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

/** defaultAvatarFor is a new agent's starting look: colour and shape from
    the handle's hash, so the same handle always gets the same avatar. */
export function defaultAvatarFor(handle: string): { shape: AvatarShape; color: string } {
  const h = hashHandle(handle);
  return { shape: AVATAR_SHAPES[Math.floor(h / AVATAR_COLORS.length) % AVATAR_SHAPES.length], color: AVATAR_COLORS[h % AVATAR_COLORS.length] };
}

/** colorInputValue turns a stored color into what <input type="color">
    accepts (#rrggbb, lowercase). A short #rgb is expanded; anything else
    (a CSS name, rgb()) shows the default rather than black. */
export function colorInputValue(c: string | null | undefined): string {
  const v = (c ?? "").trim().toLowerCase();
  if (/^#[0-9a-f]{6}$/.test(v)) return v;
  if (/^#[0-9a-f]{3}$/.test(v)) return "#" + [...v.slice(1)].map((h) => h + h).join("");
  return AVATAR_COLORS[0];
}

/* a = wobble amplitude, k = lobes, w = wobble speed, open = eye height
   (1 = normal), ey = eye vertical shift, blink = blinks every BLINK_EVERY s,
   look = eyes wander up and sideways (thinking), egg = taller, narrower,
   dot = a small satellite circles the body (orbit). */
type StateSpec = { a: number; k: number; w: number; open: number; ey: number; blink?: boolean; look?: boolean; egg?: boolean; dot?: boolean };

export const STATE_SPECS: Record<AvatarState, StateSpec> = {
  idle: { a: 0.035, k: 3, w: 1.1, open: 1, ey: 0, blink: true },
  thinking: { a: 0.09, k: 4, w: 3.6, open: 0.75, ey: -0.14, look: true },
  orbit: { a: 0.06, k: 5, w: 5, open: 1, ey: 0, dot: true },
  alert: { a: 0.03, k: 3, w: 2, open: 1.25, ey: 0 },
  notify: { a: 0.05, k: 3, w: 2.2, open: 1, ey: 0, blink: true },
  sleep: { a: 0.02, k: 2, w: 0.5, open: 0.07, ey: 0.1 },
  egg: { a: 0.015, k: 2, w: 1, open: 0.55, ey: 0.08, egg: true },
};

/* m = symmetry (0 = circle), n = superformula exponent (higher = fuller
   corners), rot turns the first lobe. */
type ShapeSpec = { m: number; n: number; rot: number };
const SHAPES: Record<AvatarShape, ShapeSpec> = {
  circle: { m: 0, n: 2, rot: 0 },
  squircle: { m: 4, n: 4.5, rot: 0 },
  triangle: { m: 3, n: 3.2, rot: -Math.PI / 2 },
  diamond: { m: 4, n: 1.35, rot: 0 },
};

export const BODY_R = 1.08;
export const BLINK_EVERY = 3.7;
const BLINK_LEN = 0.13;

export function normalizeShape(s: string | null | undefined): AvatarShape {
  return AVATAR_SHAPES.includes(s as AvatarShape) ? (s as AvatarShape) : "circle";
}

export function normalizeState(s: string | null | undefined): AvatarState {
  return AVATAR_STATES.includes(s as AvatarState) ? (s as AvatarState) : "idle";
}

/** stateFor picks the state from what the roster knows. A hatching egg
    wins over everything, then work (orbit while a tool runs), then a
    disabled agent, then the attention states. */
export function stateFor(f: { working?: boolean; tool?: boolean; asleep?: boolean; hatching?: boolean; alert?: boolean; notify?: boolean }): AvatarState {
  if (f.hatching) return "egg";
  if (f.working) return f.tool ? "orbit" : "thinking";
  if (f.asleep) return "sleep";
  if (f.alert) return "alert";
  if (f.notify) return "notify";
  return "idle";
}

/** radiusAt is the still outline radius at angle theta (superformula);
    1 everywhere for the circle. */
export function radiusAt(shape: AvatarShape, theta: number): number {
  const s = SHAPES[shape];
  if (!s.m) return 1;
  const q = (s.m * (theta - s.rot)) / 4;
  return Math.pow(Math.pow(Math.abs(Math.cos(q)), s.n) + Math.pow(Math.abs(Math.sin(q)), s.n), -1 / s.n);
}

/** blobPath returns the outline for a state at time t (seconds), scaled so
    its widest point is BODY_R. animate=false draws the still outline. */
export function blobPath(shape: AvatarShape, state: AvatarState = "idle", t = 0, animate = true, points = 64): string {
  const P = STATE_SPECS[state];
  const pts: [number, number][] = [];
  let max = 0;
  for (let i = 0; i < points; i++) {
    const th = (i / points) * 2 * Math.PI;
    const wob = animate ? 1 + P.a * Math.sin(P.k * th + t * P.w) + P.a * 0.5 * Math.sin((P.k + 2) * th - t * P.w * 0.7) : 1;
    const r = radiusAt(shape, th) * wob;
    let x = r * Math.cos(th);
    let y = r * Math.sin(th);
    if (P.egg) {
      y *= 1.18;
      x *= 0.9 + 0.1 * Math.sin(th);
    }
    pts.push([x, y]);
    max = Math.max(max, Math.hypot(x, y));
  }
  const sc = BODY_R / (max || 1);
  return pts.map(([x, y], i) => `${i ? "L" : "M"}${(x * sc).toFixed(3)} ${(y * sc).toFixed(3)}`).join("") + "Z";
}

export type Vec = { x: number; y: number };

export const DOT_R = 0.16;
const DOT_ORBIT = 1.35;

/** orbitDot is the satellite's centre at time t for a state that has one
    (orbit), null otherwise. animate=false parks it at the top right. */
export function orbitDot(state: AvatarState, t: number, animate = true): Vec | null {
  if (!STATE_SPECS[state].dot) return null;
  const a = animate ? t * 3 : -Math.PI / 4;
  return { x: DOT_ORBIT * Math.cos(a), y: DOT_ORBIT * Math.sin(a) };
}

/** thinkDots are the thinking pose's three dots at time t: fixed spots
    on the top-right edge, each pulsing (size and opacity) a beat after the
    one before. animate=false draws them all full. [] for other states. */
export function thinkDots(state: AvatarState, t: number, animate = true): { x: number; y: number; r: number; o: number }[] {
  if (state !== "thinking") return [];
  return [-1.3, -0.85, -0.4].map((a, i) => {
    const p = animate ? 0.5 + 0.5 * Math.sin(t * 5 - i * 0.9) : 1;
    return { x: 1.22 * Math.cos(a), y: 1.22 * Math.sin(a), r: 0.09 + 0.06 * p, o: 0.3 + 0.7 * p };
  });
}

/** gazeTarget turns the pointer's offset from the avatar centre (px) into
    an eye offset in unit space: full lean from 160px away, less when the
    pointer is right on top of the avatar. */
export function gazeTarget(dx: number, dy: number): Vec {
  const d = Math.hypot(dx, dy);
  if (d === 0) return { x: 0, y: 0 };
  const k = Math.min(1, d / 160);
  return { x: (dx / d) * k * 0.17, y: (dy / d) * k * 0.13 };
}

/** approach eases cur toward target by rate per frame. */
export function approach(cur: Vec, target: Vec, rate = 0.14): Vec {
  return { x: cur.x + (target.x - cur.x) * rate, y: cur.y + (target.y - cur.y) * rate };
}

/** followsPointer: a sleeping or unhatched avatar does not look around. */
export const followsPointer = (s: AvatarState) => s !== "sleep" && s !== "egg";

export type Eye = { cx: number; cy: number; rx: number; ry: number };

/** eyesAt places both eyes for a frame. gaze comes from approach();
    hover opens the eyes a little wider (attentive), wink closes the right
    one (click reaction). animate=false skips the blink and the wander. */
export function eyesAt(
  state: AvatarState,
  t: number,
  gaze: Vec = { x: 0, y: 0 },
  opts: { animate?: boolean; hover?: boolean; wink?: boolean } = {},
): [Eye, Eye] {
  const P = STATE_SPECS[state];
  const animate = opts.animate ?? true;
  const blinking = animate && P.blink && t % BLINK_EVERY < BLINK_LEN;
  let open = P.open * (blinking ? 0.1 : 1);
  if (opts.hover && state !== "sleep") open *= 1.15;
  const look = animate && P.look;
  const cy = -0.05 + P.ey + (look ? -0.12 * Math.abs(Math.sin(t * 1.3)) : 0) + gaze.y;
  const gx = (look ? 0.12 * Math.sin(t * 0.9) : 0) + gaze.x;
  const eye = (side: number, o: number): Eye => ({ cx: side + gx, cy, rx: 0.11, ry: Math.max(0.02, 0.2 * o) });
  return [eye(-0.3, open), eye(0.3, opts.wink ? 0.1 : open)];
}
