/* Idle fidget: an avatar with nothing to do now and then plays a short,
   random bit (a glance, a yawn, a hop) and settles back to breathing, so
   a waiting agent looks bored rather than frozen. After FIDGET_DROWSY_AFTER
   of idling it gets drowsy instead: half-shut eyes, slow breath, until a
   hover or a turn wakes it.

   Pure and clock-free: the caller passes the time and the conditions every
   frame, rand is injectable, so the schedule is testable with a seed. The
   runtime (core/runtime.ts) turns the returned FidgetPose into a frame. */
import type { BlobExpression, Gaze } from "../core/types";
import type { MotionSample } from "./states";

/** Seconds between two fidgets of one avatar, picked anew each time. */
export const FIDGET_MIN_GAP = 8;
export const FIDGET_MAX_GAP = 25;
/** Seconds of idling before the avatar dozes off. */
export const FIDGET_DROWSY_AFTER = 600;
/** Seconds between two fidgets while restless (a chat still loading):
    the avatar fills the wait instead of dozing through it. */
export const FIDGET_RESTLESS_MIN_GAP = 0.6;
export const FIDGET_RESTLESS_MAX_GAP = 2.5;
/** Avatars fidgeting at once, page-wide. */
export const FIDGET_MAX_ACTIVE = 3;
/** Below this size only the subtle bits (eyes) play. */
export const FIDGET_BIG_MIN = 64;

/** One frame of a fidget. Motion fields are added to the state's motion;
    gaze "cursor" asks the caller for a look at the pointer. */
export type FidgetPose = {
  expression?: BlobExpression;
  gaze?: Gaze | "cursor";
  motion?: Partial<MotionSample>;
  /** 0..1: the left eye closed (a one-eye blink). */
  wink?: number;
  /** 0..1: how drowsy (half-shut eyes, sleep breathing). */
  drowse?: number;
};

export type FidgetAction = {
  id: string;
  /** Big moves: only on avatars of FIDGET_BIG_MIN and up. */
  heavy: boolean;
  /** Relative odds; the subtle bits are the common ones. */
  weight: number;
  /** Seconds, min and max. */
  duration: [number, number];
  pose(p: number): FidgetPose;
};

const clamp01 = (v: number) => Math.min(1, Math.max(0, v));
/** bell rises and falls once over p in 0..1. */
const bell = (p: number) => Math.sin(Math.PI * clamp01(p));
/** edge eases in over the first fifth and out over the last. */
const edge = (p: number) => {
  const e = clamp01(Math.min(p, 1 - p) / 0.2);
  return e * e * (3 - 2 * e);
};

export const FIDGET_ACTIONS: readonly FidgetAction[] = [
  { id: "glance", heavy: false, weight: 5, duration: [1.8, 2.6], pose: (p) => ({ gaze: { yaw: (p < 0.5 ? -24 : 24) * edge(p), pitch: -2 * edge(p) } }) },
  { id: "wink", heavy: false, weight: 3, duration: [0.5, 0.7], pose: (p) => ({ wink: bell(p) }) },
  { id: "peek", heavy: false, weight: 2, duration: [1.2, 2], pose: () => ({ gaze: "cursor" }) },
  {
    id: "yawn",
    heavy: true,
    weight: 1,
    duration: [2.4, 3],
    pose: (p) => ({ expression: "unimpressed", motion: { squash: 0.06 * bell(p), breathe: 0.03 * bell(p), tilt: -0.06 * bell(p) } }),
  },
  {
    id: "curious",
    heavy: true,
    weight: 1.5,
    duration: [1.8, 2.6],
    pose: (p) => ({ expression: "curious", gaze: { yaw: 0, pitch: 6 * edge(p) }, motion: { tilt: 0.22 * edge(p) } }),
  },
  { id: "hop", heavy: true, weight: 1.2, duration: [1, 1.4], pose: (p) => ({ motion: { bounce: -10 * Math.abs(Math.sin(2 * Math.PI * p)) } }) },
  {
    id: "jelly",
    heavy: true,
    weight: 1,
    duration: [1.2, 1.8],
    pose: (p) => ({ motion: { jelly: 0.05 * bell(p), squash: 0.03 * Math.sin(12 * p) * bell(p) } }),
  },
  {
    id: "whistle",
    heavy: true,
    weight: 0.8,
    duration: [2.4, 3],
    pose: (p) => ({ expression: "proud", gaze: { yaw: 0, pitch: 14 * edge(p) }, motion: { tilt: 0.06 * Math.sin(4 * Math.PI * p) * edge(p) } }),
  },
];

/** fidgetActionsFor: what an avatar of this size may play. */
export function fidgetActionsFor(size: number): readonly FidgetAction[] {
  return size >= FIDGET_BIG_MIN ? FIDGET_ACTIONS : FIDGET_ACTIONS.filter((a) => !a.heavy);
}

/** pickFidget draws one action by weight; `not` (the one just played)
    is left out when there is anything else to draw. */
export function pickFidget(actions: readonly FidgetAction[], rand: () => number, not = ""): FidgetAction {
  if (not && actions.length > 1) actions = actions.filter((a) => a.id !== not);
  const total = actions.reduce((s, a) => s + a.weight, 0);
  let r = rand() * total;
  for (const a of actions) {
    r -= a.weight;
    if (r < 0) return a;
  }
  return actions[actions.length - 1];
}

/** The page-wide cap on avatars fidgeting at once. */
export type FidgetSlots = {
  acquire(owner: object): boolean;
  release(owner: object): void;
  readonly active: number;
};

export function createFidgetSlots(max = FIDGET_MAX_ACTIVE): FidgetSlots {
  const held = new Set<object>();
  return {
    acquire(owner) {
      if (held.has(owner)) return true;
      if (held.size >= max) return false;
      held.add(owner);
      return true;
    },
    release(owner) {
      held.delete(owner);
    },
    get active() {
      return held.size;
    },
  };
}

export const sharedFidgetSlots = createFidgetSlots();

/** The conditions of one frame. Busy (any state but idle: thinking,
    alert, notify, a click's wink…) resets the idle clock; the others
    only pause. */
export type FidgetEnv = {
  /** Seconds, the same clock every call. */
  time: number;
  size: number;
  busy?: boolean;
  enabled?: boolean;
  hidden?: boolean;
  offscreen?: boolean;
  reduced?: boolean;
  /** Waiting on something (a chat loading): short random gaps, no
      repeat of the last bit, never drowsy. */
  restless?: boolean;
};

/** fidgetPaused: no fidget and no drowsing this frame. */
export const fidgetPaused = (e: FidgetEnv) =>
  !!e.busy || e.enabled === false || !!e.hidden || !!e.offscreen || !!e.reduced;

export type Fidget = {
  /** step returns this frame's pose, or null for plain idle. */
  step(env: FidgetEnv): FidgetPose | null;
  /** wake restarts the idle clock (a hover): a dozing avatar wakes up. */
  wake(time: number): void;
  /** stop ends a running fidget and frees its slot (scrolled away,
      unmounted). The next step starts over with a fresh wait. */
  stop(): void;
  /** The running action's id, "drowsy", or "". */
  readonly current: string;
};

/** Seeded generator (mulberry32) for tests and reproducible schedules. */
export function seededRand(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function createFidget(o: { now: number; rand?: () => number; slots?: FidgetSlots }): Fidget {
  const rand = o.rand ?? Math.random;
  const slots = o.slots ?? sharedFidgetSlots;
  const owner = {};
  let restless = false;
  const gap = () =>
    restless
      ? FIDGET_RESTLESS_MIN_GAP + rand() * (FIDGET_RESTLESS_MAX_GAP - FIDGET_RESTLESS_MIN_GAP)
      : FIDGET_MIN_GAP + rand() * (FIDGET_MAX_GAP - FIDGET_MIN_GAP);
  let last = "";

  let idleSince = o.now;
  // Each avatar draws its own first wait, so a page of them never moves
  // in step.
  let nextAt = o.now + gap();
  let paused = false;
  let drowsy = false;
  let active: { action: FidgetAction; start: number; dur: number } | null = null;

  function end() {
    active = null;
    slots.release(owner);
  }

  return {
    step(env) {
      const t = env.time;
      restless = !!env.restless;
      if (env.busy || restless) idleSince = t;
      if (fidgetPaused(env)) {
        end();
        drowsy = false;
        paused = true;
        return null;
      }
      if (paused) {
        // Back from a pause: a fresh wait, never a fidget the same frame.
        paused = false;
        nextAt = t + gap();
      }
      if (t - idleSince >= FIDGET_DROWSY_AFTER) {
        end();
        drowsy = true;
        return { drowse: 1 };
      }
      if (drowsy) {
        drowsy = false;
        nextAt = t + gap();
      }
      if (active) {
        const p = (t - active.start) / active.dur;
        if (p < 1) return active.action.pose(Math.max(0, p));
        end();
        nextAt = t + gap();
      }
      // Turning restless mid-wait cuts a long idle gap short.
      if (restless && nextAt - t > FIDGET_RESTLESS_MAX_GAP) nextAt = t + gap();
      if (t < nextAt) return null;
      if (!slots.acquire(owner)) {
        // The page is busy fidgeting: try again shortly.
        nextAt = t + 2 + rand() * 3;
        return null;
      }
      const action = pickFidget(fidgetActionsFor(env.size), rand, restless ? last : "");
      last = action.id;
      const [lo, hi] = action.duration;
      active = { action, start: t, dur: lo + rand() * (hi - lo) };
      return action.pose(0);
    },
    wake(time) {
      idleSince = time;
    },
    stop() {
      end();
      drowsy = false;
      paused = true;
    },
    get current() {
      return active ? active.action.id : drowsy ? "drowsy" : "";
    },
  };
}
