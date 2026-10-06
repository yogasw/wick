import type { BlobShape } from "../core/types";

export type Point = { x: number; y: number };

export const POINT_COUNT = 32;

function polar(radiusAt: (angle: number) => number): Point[] {
  return Array.from({ length: POINT_COUNT }, (_, i) => {
    const angle = (i / POINT_COUNT) * Math.PI * 2 - Math.PI / 2;
    const radius = radiusAt(angle);
    return { x: Math.cos(angle) * radius, y: Math.sin(angle) * radius };
  });
}

function rayCircle(angle: number, cx: number, cy: number, radius: number): number {
  const dx = Math.cos(angle);
  const dy = Math.sin(angle);
  const b = dx * cx + dy * cy;
  const disc = b * b - (cx * cx + cy * cy - radius * radius);
  if (disc < 0) return 0;
  return Math.max(0, b + Math.sqrt(disc));
}

function unionCircles(lobes: Array<{ x: number; y: number; r: number }>): Point[] {
  return polar((angle) => {
    let best = 0;
    for (const lobe of lobes) {
      best = Math.max(best, rayCircle(angle, lobe.x, lobe.y, lobe.r));
    }
    return best || 0.35;
  });
}

function superellipse(power: number, sx: number, sy: number): Point[] {
  return polar((angle) => {
    const c = Math.cos(angle);
    const s = Math.sin(angle);
    return 1 / Math.pow(Math.pow(Math.abs(c) / sx, power) + Math.pow(Math.abs(s) / sy, power), 1 / power);
  });
}

function roundedPolygon(sides: number, size: number, round: number, turn = -Math.PI / 2): Point[] {
  const sector = (Math.PI * 2) / sides;
  return polar((angle) => {
    const shifted = ((angle - turn) % (Math.PI * 2) + Math.PI * 2) % (Math.PI * 2);
    const local = shifted - sector * Math.floor(shifted / sector) - sector / 2;
    const edge = Math.cos(sector / 2) / Math.max(0.18, Math.cos(local));
    return size * (edge * (1 - round) + round);
  });
}

function teardrop(): Point[] {
  return Array.from({ length: POINT_COUNT }, (_, i) => {
    const angle = (i / POINT_COUNT) * Math.PI * 2 - Math.PI / 2;
    const s = Math.sin(angle);
    const c = Math.cos(angle);
    const u = (s + 1) / 2;
    const w = Math.pow(u, 0.46);
    return {
      x: c * 0.7 * w,
      y: s < 0 ? s * 0.92 : s * 0.7,
    };
  });
}

function stadium(halfW: number, halfH: number): Point[] {
  const cap = halfH;
  const inner = Math.max(0.01, halfW - cap);
  return polar((angle) => {
    const c = Math.cos(angle);
    const s = Math.sin(angle);
    if (Math.abs(s) < 1e-4) return halfW / Math.max(0.05, Math.abs(c));
    const tFlat = cap / Math.abs(s);
    if (Math.abs(tFlat * c) <= inner) return tFlat;
    return rayCircle(angle, Math.sign(c || 1) * inner, 0, cap);
  });
}

export type ShapeFace = {
  y: number;
  scale: number;
};

export const SHAPE_FACE: Record<BlobShape, ShapeFace> = {
  circle: { y: 0, scale: 1.08 },
  pebble: { y: 0, scale: 1.04 },
  squircle: { y: 0, scale: 1.04 },
  capsule: { y: 0, scale: 0.88 },
  triangle: { y: 0.14, scale: 0.74 },
  cloud: { y: 0.02, scale: 0.9 },
  droplet: { y: 0.2, scale: 0.68 },
  flame: { y: 0.18, scale: 0.74 },
  medal: { y: -0.2, scale: 0.74 },
  acorn: { y: 0.02, scale: 0.86 },
  jellyfish: { y: -0.24, scale: 0.7 },
  clover: { y: 0, scale: 0.72 },
};

const SHAPE_POINTS: Record<BlobShape, Point[]> = {
  circle: polar(() => 0.78),
  pebble: polar((angle) => 0.74 + 0.08 * Math.sin(2 * angle + 0.5) + 0.05 * Math.sin(3 * angle + 1.1) + 0.03 * Math.cos(angle - 0.4)),
  squircle: superellipse(5.2, 0.78, 0.78),
  capsule: stadium(0.92, 0.54),
  triangle: roundedPolygon(3, 0.9, 0.5),
  cloud: unionCircles([
    { x: -0.32, y: 0.06, r: 0.5 },
    { x: 0.02, y: -0.22, r: 0.54 },
    { x: 0.34, y: 0.06, r: 0.5 },
    { x: 0, y: 0.24, r: 0.5 },
  ]),
  droplet: teardrop(),
  flame: polar((angle) => {
    const lean = angle - 0.22;
    const tip = Math.max(0, 0.5 + 0.5 * Math.sin(lean));
    const top = Math.max(0, -Math.sin(lean));
    return 0.4 + 0.4 * Math.pow(tip, 1.08) + 0.1 * top + 0.03 * Math.sin(2 * lean);
  }),
  medal: unionCircles([
    { x: 0, y: -0.14, r: 0.64 },
    { x: -0.28, y: 0.48, r: 0.34 },
    { x: 0.28, y: 0.48, r: 0.34 },
  ]),
  acorn: polar((angle) => 0.72 + 0.08 * Math.sin(angle) + 0.02 * Math.cos(2 * angle)),
  jellyfish: unionCircles([
    { x: 0, y: -0.16, r: 0.62 },
    { x: -0.24, y: 0.5, r: 0.3 },
    { x: 0.24, y: 0.5, r: 0.3 },
  ]),
  clover: unionCircles([
    { x: 0, y: -0.28, r: 0.48 },
    { x: 0.28, y: 0, r: 0.48 },
    { x: 0, y: 0.28, r: 0.48 },
    { x: -0.28, y: 0, r: 0.48 },
  ]),
};

export function getShapePoints(shape: BlobShape): Point[] {
  return SHAPE_POINTS[shape].map((point) => ({ ...point }));
}

export function lerpPoints(from: Point[], to: Point[], t: number): Point[] {
  const count = Math.min(from.length, to.length);
  return Array.from({ length: count }, (_, i) => ({
    x: from[i].x + (to[i].x - from[i].x) * t,
    y: from[i].y + (to[i].y - from[i].y) * t,
  }));
}

export function toCanvasPoints(
  points: Point[],
  cx: number,
  cy: number,
  radius: number,
): Point[] {
  return points.map((point) => ({
    x: cx + point.x * radius,
    y: cy + point.y * radius,
  }));
}
