import { describe, test, expect } from "vitest";
import {
  AVATAR_COLORS,
  AVATAR_SHAPES,
  AVATAR_STATES,
  AVATAR_STATE_LABELS,
  BLINK_EVERY,
  BODY_R,
  approach,
  blobPath,
  colorInputValue,
  eyesAt,
  followsPointer,
  gazeTarget,
  normalizeShape,
  normalizeState,
  radiusAt,
  stateFor,
  orbitDot,
  hashHandle,
  defaultAvatarFor,
} from "../shape.js";

/* Parse "M x yL x y…Z" back into points. */
function points(d: string): [number, number][] {
  return [...d.matchAll(/[ML](-?[\d.]+) (-?[\d.]+)/g)].map((m) => [Number(m[1]), Number(m[2])]);
}
const maxRadius = (d: string) => Math.max(...points(d).map(([x, y]) => Math.hypot(x, y)));

describe("normalize", () => {
  test("unknown shape or state falls back to the default", () => {
    expect(normalizeShape("hexagon")).toBe("circle");
    expect(normalizeShape(undefined)).toBe("circle");
    expect(normalizeShape("diamond")).toBe("diamond");
    expect(normalizeState("comet")).toBe("idle");
    expect(normalizeState("orbit")).toBe("orbit");
    expect(normalizeState("alert")).toBe("alert");
  });
});

describe("stateFor", () => {
  test("egg beats work beats sleep beats attention", () => {
    expect(stateFor({ hatching: true, working: true, asleep: true })).toBe("egg");
    expect(stateFor({ working: true, asleep: true, alert: true })).toBe("thinking");
    expect(stateFor({ working: true, tool: true, asleep: true })).toBe("orbit");
    // A tool name left over from a finished turn does not keep it orbiting.
    expect(stateFor({ tool: true })).toBe("idle");
    expect(stateFor({ asleep: true, notify: true })).toBe("sleep");
    expect(stateFor({ alert: true, notify: true })).toBe("alert");
    expect(stateFor({ notify: true })).toBe("notify");
    expect(stateFor({})).toBe("idle");
  });
});

describe("radiusAt", () => {
  test("the circle is round, the other shapes are not", () => {
    expect(radiusAt("circle", 1.234)).toBe(1);
    for (const s of ["squircle", "triangle", "diamond"] as const) {
      const rs = Array.from({ length: 36 }, (_, i) => radiusAt(s, (i / 36) * 2 * Math.PI));
      expect(Math.max(...rs) - Math.min(...rs)).toBeGreaterThan(0.01);
    }
  });
});

describe("blobPath", () => {
  test("every shape and state closes at BODY_R", () => {
    for (const shape of AVATAR_SHAPES) {
      for (const st of AVATAR_STATES) {
        for (const t of [0, 0.7, 5.3]) {
          const d = blobPath(shape, st, t);
          expect(d.startsWith("M")).toBe(true);
          expect(d.endsWith("Z")).toBe(true);
          expect(points(d)).toHaveLength(64);
          expect(maxRadius(d)).toBeCloseTo(BODY_R, 2);
        }
      }
    }
  });

  test("still when animate is off, alive when on", () => {
    expect(blobPath("squircle", "thinking", 0, false)).toBe(blobPath("squircle", "thinking", 3, false));
    expect(blobPath("squircle", "thinking", 0)).not.toBe(blobPath("squircle", "thinking", 0.5));
  });

  test("the egg is taller than it is wide", () => {
    const pts = points(blobPath("circle", "egg", 0, false));
    const w = Math.max(...pts.map((p) => p[0])) - Math.min(...pts.map((p) => p[0]));
    const h = Math.max(...pts.map((p) => p[1])) - Math.min(...pts.map((p) => p[1]));
    expect(h).toBeGreaterThan(w * 1.15);
  });
});

describe("gaze", () => {
  test("leans toward the pointer, capped, and none at the centre", () => {
    expect(gazeTarget(0, 0)).toEqual({ x: 0, y: 0 });
    const far = gazeTarget(1000, 0);
    expect(far.x).toBeCloseTo(0.17);
    expect(far.y).toBeCloseTo(0);
    const near = gazeTarget(40, 0);
    expect(near.x).toBeGreaterThan(0);
    expect(near.x).toBeLessThan(far.x);
    expect(gazeTarget(0, -1000).y).toBeCloseTo(-0.13);
  });

  test("approach eases and converges", () => {
    let g = { x: 0, y: 0 };
    g = approach(g, { x: 1, y: -1 });
    expect(g.x).toBeCloseTo(0.14);
    for (let i = 0; i < 200; i++) g = approach(g, { x: 1, y: -1 });
    expect(g.x).toBeCloseTo(1, 3);
    expect(g.y).toBeCloseTo(-1, 3);
  });

  test("a sleeping or unhatched avatar does not follow", () => {
    expect(followsPointer("idle")).toBe(true);
    expect(followsPointer("thinking")).toBe(true);
    expect(followsPointer("sleep")).toBe(false);
    expect(followsPointer("egg")).toBe(false);
  });
});

describe("eyesAt", () => {
  const open = (st: Parameters<typeof eyesAt>[0], t = 1, o = {}) => eyesAt(st, t, undefined, o)[0].ry;

  test("two eyes, mirrored, shifted together by the gaze", () => {
    const [l, r] = eyesAt("idle", 1, { x: 0.1, y: 0.05 });
    expect(l.cx).toBeCloseTo(-0.2);
    expect(r.cx).toBeCloseTo(0.4);
    expect(l.cy).toBe(r.cy);
    expect(l.cy).toBeCloseTo(0);
  });

  test("idle blinks once per cycle, never with motion off", () => {
    expect(open("idle", BLINK_EVERY * 2 + 0.05)).toBeLessThan(0.05);
    expect(open("idle", BLINK_EVERY * 2 + 1)).toBeCloseTo(0.2);
    expect(open("idle", BLINK_EVERY * 2 + 0.05, { animate: false })).toBeCloseTo(0.2);
  });

  test("alert is wide, sleep is shut, hover opens a little more", () => {
    expect(open("alert")).toBeGreaterThan(open("idle"));
    expect(open("sleep")).toBeLessThan(0.03);
    expect(open("idle", 1, { hover: true })).toBeGreaterThan(open("idle"));
    expect(open("sleep", 1, { hover: true })).toBe(open("sleep"));
  });

  test("thinking looks up; a wink closes only the right eye", () => {
    expect(eyesAt("thinking", 1)[0].cy).toBeLessThan(eyesAt("idle", 1)[0].cy);
    const [l, r] = eyesAt("idle", 1, undefined, { wink: true });
    expect(l.ry).toBeCloseTo(0.2);
    expect(r.ry).toBeLessThan(0.05);
  });
});

describe("colorInputValue", () => {
  test("passes a #rrggbb through, lowercased", () => {
    expect(colorInputValue("#27B199")).toBe("#27b199");
  });
  test("expands a short #rgb", () => {
    expect(colorInputValue("#0aF")).toBe("#00aaff");
  });
  test("falls back to the default for what the picker cannot show", () => {
    expect(colorInputValue("tomato")).toBe(AVATAR_COLORS[0]);
    expect(colorInputValue("")).toBe(AVATAR_COLORS[0]);
    expect(colorInputValue(undefined)).toBe(AVATAR_COLORS[0]);
  });
});

test("every avatar state has a grid caption", () => {
  for (const s of AVATAR_STATES) expect(AVATAR_STATE_LABELS[s]).toBeTruthy();
});

describe("orbit", () => {
  test("only orbit has a satellite, and it circles outside the body", () => {
    for (const st of AVATAR_STATES) {
      if (st !== "orbit") expect(orbitDot(st, 1)).toBeNull();
    }
    const a = orbitDot("orbit", 0)!;
    const b = orbitDot("orbit", 0.5)!;
    expect(Math.hypot(a.x, a.y)).toBeGreaterThan(BODY_R);
    expect(a).not.toEqual(b);
    expect(orbitDot("orbit", 0, false)).toEqual(orbitDot("orbit", 9, false));
  });
});

describe("defaultAvatarFor", () => {
  test("FNV-1a matches the server's team.DefaultAvatarFor vectors", () => {
    expect(hashHandle("log-hunter")).toBe(728470498);
    expect(defaultAvatarFor("log-hunter")).toEqual({ shape: AVATAR_SHAPES[0], color: AVATAR_COLORS[2] });
    expect(defaultAvatarFor("captain")).toEqual({ shape: AVATAR_SHAPES[1], color: AVATAR_COLORS[3] });
  });
  test("is deterministic and spreads across the palette", () => {
    expect(defaultAvatarFor("x-1")).toEqual(defaultAvatarFor("x-1"));
    const colors = new Set(["a", "b", "c", "d", "e", "f", "g", "h"].map((h) => defaultAvatarFor(h).color));
    expect(colors.size).toBeGreaterThan(2);
  });
});
