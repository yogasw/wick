import { formatHex, lerpRgb, parseHex, type Rgb } from "./color";
import { getFace, lerpFace, type FacePose } from "../face/expressions";
import { getShapePoints, lerpPoints, type Point } from "../geometry/shapes";
import { sampleMotion, type MotionSample } from "../motion/states";
import type { BlobShape, BlobSnapshot, BlobState, Gaze } from "./types";

export type VisualFrame = {
  points: Point[];
  face: FacePose;
  color: string;
  gaze: Gaze;
  motion: MotionSample;
  blink: number;
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

export function createRuntime(initial: BlobSnapshot) {
  let points = getShapePoints(initial.shape);
  let face = getFace(initial.expression);
  let rgb: Rgb = parseHex(initial.color);
  let gaze: Gaze = { ...initial.gaze };
  let nextBlink = 1.6 + Math.random() * 1.4;
  let blinkStart = -1;

  return {
    step(snapshot: BlobSnapshot, dt: number, time: number): VisualFrame {
      const t = damp(Math.min(dt, 0.05), 9);
      points = lerpPoints(points, getShapePoints(snapshot.shape), t);
      face = lerpFace(face, getFace(snapshot.expression), t);
      rgb = lerpRgb(rgb, parseHex(snapshot.color), t);
      const look = damp(Math.min(dt, 0.05), 11);
      gaze = {
        yaw: gaze.yaw + (snapshot.gaze.yaw - gaze.yaw) * look,
        pitch: gaze.pitch + (snapshot.gaze.pitch - gaze.pitch) * look,
      };

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
        motion: sampleMotion(snapshot.state, time),
        blink,
        state: snapshot.state,
        shape: snapshot.shape,
      };
    },
  };
}
