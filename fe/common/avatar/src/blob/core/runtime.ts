import { formatHex, lerpRgb, parseHex, type Rgb } from "./color";
import { getFace, lerpFace, type FacePose } from "../face/expressions";
import { getShapePoints, lerpPoints, type Point } from "../geometry/shapes";
import { sampleMotion, type MotionSample } from "../motion/states";
import type { FidgetPose } from "../motion/fidget";
import type { BlobShape, BlobSnapshot, BlobState, Gaze } from "./types";

export type VisualFrame = {
  points: Point[];
  face: FacePose;
  color: string;
  gaze: Gaze;
  motion: MotionSample;
  blink: number;
  /** 0..1: the left eye alone closed (an idle fidget's wink). */
  wink: number;
  state: BlobState;
  shape: BlobShape;
};

function damp(dt: number, speed: number): number {
  return 1 - Math.exp(-speed * dt);
}

function blinkAmount(phase: number): number {
  if (phase <= 0 || phase >= 1) return 0;
  if (phase < 0.35) return phase / 0.35;
  if (phase < 0.55) return 1;
  return 1 - (phase - 0.55) / 0.45;
}

/** withFidget adds a fidget's motion on top of the state's; drowse
    eases the idle breath into the slow sleep one. */
function withFidget(m: MotionSample, time: number, drowse: number, fidget?: FidgetPose | null): MotionSample {
  let out = m;
  if (drowse > 0.001) {
    const z = sampleMotion("sleep", time * 0.8);
    const mix = (a: number, b: number) => a + (b - a) * drowse;
    out = {
      breathe: mix(out.breathe, z.breathe),
      squash: mix(out.squash, z.squash),
      tilt: mix(out.tilt, z.tilt),
      jelly: mix(out.jelly, z.jelly),
      bounce: mix(out.bounce, z.bounce),
    };
  }
  const d = fidget?.motion;
  if (!d) return out;
  return {
    breathe: out.breathe + (d.breathe ?? 0),
    squash: out.squash + (d.squash ?? 0),
    tilt: out.tilt + (d.tilt ?? 0),
    jelly: out.jelly + (d.jelly ?? 0),
    bounce: out.bounce + (d.bounce ?? 0),
  };
}

export function createRuntime(initial: BlobSnapshot) {
  let points = getShapePoints(initial.shape);
  let face = getFace(initial.expression);
  let rgb: Rgb = parseHex(initial.color);
  let gaze: Gaze = { ...initial.gaze };
  let nextBlink = 1.6 + Math.random() * 1.4;
  let blinkStart = -1;
  let drowse = 0;

  return {
    /** fidget (idle only, see motion/fidget.ts) borrows the expression and
        the gaze for a moment and adds to the motion; the usual damping
        eases it in and out. A gaze "cursor" is the caller's to resolve. */
    step(snapshot: BlobSnapshot, dt: number, time: number, fidget?: FidgetPose | null): VisualFrame {
      const t = damp(Math.min(dt, 0.05), 9);
      points = lerpPoints(points, getShapePoints(snapshot.shape), t);
      face = lerpFace(face, getFace(fidget?.expression ?? snapshot.expression), t);
      rgb = lerpRgb(rgb, parseHex(snapshot.color), t);
      const look = damp(Math.min(dt, 0.05), 11);
      // The pointer, when followed, wins over a fidget's glance.
      const away = snapshot.gaze.yaw !== 0 || snapshot.gaze.pitch !== 0;
      const want = !away && fidget?.gaze && fidget.gaze !== "cursor" ? fidget.gaze : snapshot.gaze;
      gaze = {
        yaw: gaze.yaw + (want.yaw - gaze.yaw) * look,
        pitch: gaze.pitch + (want.pitch - gaze.pitch) * look,
      };
      drowse += ((fidget?.drowse ?? 0) - drowse) * damp(Math.min(dt, 0.05), 1.5);

      if (blinkStart < 0 && time >= nextBlink) blinkStart = time;
      const blink =
        blinkStart < 0 ? 0 : blinkAmount((time - blinkStart) / 0.14);
      if (blinkStart >= 0 && time - blinkStart > 0.14) {
        blinkStart = -1;
        nextBlink = time + 2.4 + Math.random() * 2.6;
      }

      return {
        points,
        face,
        color: formatHex(rgb),
        gaze,
        motion: withFidget(sampleMotion(snapshot.state, time), time, drowse, fidget),
        // Drowsy: the lids hang half shut between blinks.
        blink: Math.max(blink, drowse * 0.55),
        wink: fidget?.wink ?? 0,
        state: snapshot.state,
        shape: snapshot.shape,
      };
    },
  };
}
