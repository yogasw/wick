/* Geometry for AgentAvatar's blob. A pure function of (shape, time) so the
   component stays a thin renderer and the outline can be tested without a
   DOM. This is a light imitation of the bloub engine (PLAN 6.5), not a
   port of it: a rounded polygon whose radius wobbles a little while the
   agent works. */

export type AvatarShape = "circle" | "squircle" | "triangle" | "diamond";
export const AVATAR_SHAPES: AvatarShape[] = ["circle", "squircle", "triangle", "diamond"];

/* Palette offered in Settings → Avatar. Free hex is still accepted from the
   server; these are just the swatches. */
export const AVATAR_COLORS = [
  "#6366f1", "#27b199", "#0ea5e9", "#f59e0b", "#ef4444", "#ec4899", "#8b5cf6", "#64748b",
];

type ShapeSpec = { sides: number; rotate: number; round: number };

/* sides=0 is a circle. round blends the polygon toward its circumcircle:
   0 = sharp corners, 1 = circle. rotate turns the first vertex. */
const SPECS: Record<AvatarShape, ShapeSpec> = {
  circle: { sides: 0, rotate: 0, round: 1 },
  squircle: { sides: 4, rotate: Math.PI / 4, round: 0.55 },
  triangle: { sides: 3, rotate: -Math.PI / 2, round: 0.3 },
  diamond: { sides: 4, rotate: 0, round: 0.25 },
};

export function normalizeShape(s: string | null | undefined): AvatarShape {
  return AVATAR_SHAPES.includes(s as AvatarShape) ? (s as AvatarShape) : "circle";
}

/** radiusAt is the unit outline radius (≤1) at angle theta. */
export function radiusAt(shape: AvatarShape, theta: number): number {
  const s = SPECS[shape];
  if (s.sides === 0) return 1;
  const seg = (2 * Math.PI) / s.sides;
  let a = (theta - s.rotate) % seg;
  if (a < 0) a += seg;
  const poly = Math.cos(Math.PI / s.sides) / Math.cos(a - seg / 2);
  return poly + (1 - poly) * s.round;
}

/** blobPath returns an SVG path for the outline centred on (cx, cy) with
    outer radius r. t is seconds; working=false draws the still outline. */
export function blobPath(
  shape: AvatarShape,
  cx: number,
  cy: number,
  r: number,
  t = 0,
  working = false,
  points = 48,
): string {
  const parts: string[] = [];
  for (let i = 0; i < points; i++) {
    const th = (i / points) * 2 * Math.PI;
    const wobble = working ? 1 + 0.045 * Math.sin(3 * th + t * 2.4) + 0.025 * Math.sin(5 * th - t * 1.7) : 1;
    const rr = r * radiusAt(shape, th) * wobble;
    const x = cx + rr * Math.cos(th);
    const y = cy + rr * Math.sin(th);
    parts.push(`${i === 0 ? "M" : "L"}${x.toFixed(2)} ${y.toFixed(2)}`);
  }
  return parts.join("") + "Z";
}
