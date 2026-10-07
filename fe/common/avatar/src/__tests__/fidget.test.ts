import { describe, test, expect } from "vitest";
import {
  createFidget, createFidgetSlots, fidgetActionsFor, pickFidget, seededRand, fidgetPaused,
  FIDGET_ACTIONS, FIDGET_BIG_MIN, FIDGET_DROWSY_AFTER, FIDGET_MAX_GAP, FIDGET_MIN_GAP, type Fidget, type FidgetEnv,
} from "../blob/motion/fidget";
import { createRuntime } from "../blob/core/runtime";
import { snapshotOf } from "../blob.js";

const BIG = 72;
const SMALL = 36;

/** run steps f from `from` to `to` seconds at 10 fps and returns the
    seconds at which each fidget started, with its id. */
function run(f: Fidget, from: number, to: number, env: Omit<FidgetEnv, "time"> = { size: BIG }) {
  const starts: { t: number; id: string }[] = [];
  let prev = "";
  for (let i = Math.round(from * 10); i <= Math.round(to * 10); i++) {
    const t = i / 10;
    f.step({ ...env, time: t });
    if (f.current && f.current !== prev && f.current !== "drowsy") starts.push({ t, id: f.current });
    prev = f.current;
  }
  return starts;
}

describe("fidget schedule", () => {
  test("a seed gives the same schedule, gaps between 8 and 25 s", () => {
    const a = run(createFidget({ now: 0, rand: seededRand(7), slots: createFidgetSlots() }), 0, 300);
    const b = run(createFidget({ now: 0, rand: seededRand(7), slots: createFidgetSlots() }), 0, 300);
    expect(a).toEqual(b);
    expect(a.length).toBeGreaterThan(5);
    expect(a[0].t).toBeGreaterThanOrEqual(FIDGET_MIN_GAP);
    expect(a[0].t).toBeLessThanOrEqual(FIDGET_MAX_GAP + 0.1);
    for (let i = 1; i < a.length; i++) {
      // gap is counted from the end of the previous fidget (≤ 3 s long).
      expect(a[i].t - a[i - 1].t).toBeGreaterThanOrEqual(FIDGET_MIN_GAP);
      expect(a[i].t - a[i - 1].t).toBeLessThanOrEqual(FIDGET_MAX_GAP + 3.2);
    }
  });

  test("avatars are jittered: different seeds start apart", () => {
    const firsts = [1, 2, 3, 4, 5].map((s) => run(createFidget({ now: 0, rand: seededRand(s), slots: createFidgetSlots() }), 0, 30)[0]?.t);
    expect(new Set(firsts).size).toBeGreaterThan(3);
  });

  test("a small avatar only plays the subtle bits", () => {
    expect(fidgetActionsFor(SMALL).every((a) => !a.heavy)).toBe(true);
    expect(fidgetActionsFor(FIDGET_BIG_MIN).some((a) => a.heavy)).toBe(true);
    const ids = run(createFidget({ now: 0, rand: seededRand(3), slots: createFidgetSlots() }), 0, 599, { size: SMALL }).map((s) => s.id);
    const light = new Set(FIDGET_ACTIONS.filter((a) => !a.heavy).map((a) => a.id));
    expect(ids.length).toBeGreaterThan(10);
    expect(ids.every((id) => light.has(id))).toBe(true);
  });

  test("subtle bits outnumber big moves on a big avatar", () => {
    const rand = seededRand(11);
    const acts = fidgetActionsFor(BIG);
    let heavy = 0;
    for (let i = 0; i < 2000; i++) if (pickFidget(acts, rand).heavy) heavy++;
    expect(heavy).toBeGreaterThan(200);
    expect(heavy).toBeLessThan(1000);
  });

  test("busy never fidgets and restarts the idle clock", () => {
    const f = createFidget({ now: 0, rand: seededRand(5), slots: createFidgetSlots() });
    for (let t = 0; t < 120; t += 0.1) expect(f.step({ time: t, size: BIG, busy: true })).toBeNull();
    expect(f.current).toBe("");
    // Idle again: the first fidget waits a fresh gap.
    const s = run(f, 120, 160);
    expect(s[0].t).toBeGreaterThanOrEqual(120 + FIDGET_MIN_GAP);
  });

  test.each([
    ["hidden", { hidden: true }],
    ["reduced motion", { reduced: true }],
    ["off screen", { offscreen: true }],
    ["switched off", { enabled: false }],
  ] as const)("%s: nothing plays", (_, cond) => {
    const slots = createFidgetSlots();
    const f = createFidget({ now: 0, rand: seededRand(9), slots });
    for (let t = 0; t < 700; t += 0.5) expect(f.step({ time: t, size: BIG, ...cond })).toBeNull();
    expect(f.current).toBe("");
    expect(slots.active).toBe(0);
    expect(fidgetPaused({ time: 0, size: BIG, ...cond })).toBe(true);
  });

  test("a pause mid-fidget frees the slot", () => {
    const slots = createFidgetSlots();
    const f = createFidget({ now: 0, rand: seededRand(1), slots });
    let t = 0;
    while (!f.current && t < 30) f.step({ time: (t += 0.1), size: BIG });
    expect(f.current).not.toBe("");
    expect(slots.active).toBe(1);
    f.step({ time: t + 0.1, size: BIG, hidden: true });
    expect(slots.active).toBe(0);
    f.step({ time: t + 0.2, size: BIG });
    expect(f.current).toBe("");
  });

  test("at most three avatars fidget at once", () => {
    const slots = createFidgetSlots();
    const fs = Array.from({ length: 8 }, (_, i) => createFidget({ now: 0, rand: seededRand(i + 1), slots }));
    let most = 0;
    for (let t = 0; t < 300; t += 0.1) {
      for (const f of fs) f.step({ time: t, size: BIG });
      most = Math.max(most, fs.filter((f) => f.current && f.current !== "drowsy").length);
      expect(slots.active).toBeLessThanOrEqual(3);
    }
    expect(most).toBe(3);
  });

  test("idle past ten minutes dozes; a hover wakes it", () => {
    const f = createFidget({ now: 0, rand: seededRand(2), slots: createFidgetSlots() });
    run(f, 0, FIDGET_DROWSY_AFTER - 0.1);
    expect(f.step({ time: FIDGET_DROWSY_AFTER + 1, size: BIG })).toEqual({ drowse: 1 });
    expect(f.current).toBe("drowsy");
    expect(run(f, FIDGET_DROWSY_AFTER + 1, FIDGET_DROWSY_AFTER + 120)).toEqual([]);
    f.wake(FIDGET_DROWSY_AFTER + 120);
    expect(f.step({ time: FIDGET_DROWSY_AFTER + 120.1, size: BIG })).toBeNull();
    expect(f.current).toBe("");
  });
});

describe("runtime with a fidget", () => {
  const look = { shape: "circle", expression: "neutral", color: "#4b8fea" } as const;

  test("motion adds up, wink and drowse reach the frame", () => {
    const rt = createRuntime(snapshotOf(look));
    const snap = snapshotOf(look);
    const plain = createRuntime(snap).step(snap, 0.016, 1);
    const hop = rt.step(snap, 0.016, 1, { motion: { bounce: -10 }, wink: 1 });
    expect(hop.motion.bounce).toBeCloseTo(plain.motion.bounce - 10);
    expect(hop.wink).toBe(1);
    let f = hop;
    for (let i = 0; i < 200; i++) f = rt.step(snap, 0.05, 2 + i * 0.05, { drowse: 1 });
    expect(f.blink).toBeGreaterThan(0.5);
  });

  test("a followed pointer wins over a fidget's glance", () => {
    const rt = createRuntime(snapshotOf(look));
    let f = rt.step(snapshotOf(look, "idle", { yaw: -30, pitch: 0 }), 1, 0, { gaze: { yaw: 24, pitch: 0 } });
    for (let i = 0; i < 40; i++) f = rt.step(snapshotOf(look, "idle", { yaw: -30, pitch: 0 }), 0.05, i * 0.05, { gaze: { yaw: 24, pitch: 0 } });
    expect(f.gaze.yaw).toBeLessThan(-20);
  });
});

describe("Idle animations switch", () => {
  test("defaults on and flips page-wide", async () => {
    const { idleAnimationsOn, setIdleAnimations } = await import("../idle.js");
    expect(idleAnimationsOn()).toBe(true);
    setIdleAnimations(false);
    expect(idleAnimationsOn()).toBe(false);
    setIdleAnimations(true);
  });
});

describe("restless (a chat loading)", () => {
  test("fidgets every few seconds, never the same bit twice in a row, never dozes", () => {
    const f = createFidget({ now: 0, rand: seededRand(7), slots: createFidgetSlots(10) });
    const starts = run(f, 0, FIDGET_DROWSY_AFTER + 30, { size: BIG, restless: true });
    expect(starts.length).toBeGreaterThan(100);
    for (let i = 1; i < starts.length; i++) expect(starts[i].id).not.toBe(starts[i - 1].id);
    expect(new Set(starts.map((s) => s.id)).size).toBeGreaterThan(4);
    expect(f.current).not.toBe("drowsy");
  });

  test("turning restless cuts a long idle wait short; reduced motion still wins", () => {
    const f = createFidget({ now: 0, rand: () => 0.99, slots: createFidgetSlots(10) });
    f.step({ time: 0, size: BIG });
    expect(run(f, 0.1, 4, { size: BIG, restless: true }).length).toBeGreaterThan(0);
    const g = createFidget({ now: 0, rand: seededRand(3), slots: createFidgetSlots(10) });
    expect(run(g, 0, 60, { size: BIG, restless: true, reduced: true })).toEqual([]);
  });
});
