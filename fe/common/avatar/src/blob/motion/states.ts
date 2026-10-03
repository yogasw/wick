import type { BlobState } from "../core/types";

export type MotionSample = {
  breathe: number;
  squash: number;
  tilt: number;
  jelly: number;
  bounce: number;
};

export function sampleMotion(state: BlobState, time: number): MotionSample {
  switch (state) {
    case "thinking":
      return {
        breathe: 1 + Math.sin(time * 2.4) * 0.018,
        squash: 1 + Math.sin(time * 2.4) * 0.02,
        tilt: Math.sin(time * 1.3) * 0.08,
        jelly: 0.018,
        bounce: Math.sin(time * 1.1) * 2,
      };
    case "sleep":
      return {
        breathe: 1 + Math.sin(time * 0.9) * 0.03,
        squash: 0.94 + Math.sin(time * 0.9) * 0.02,
        tilt: 0.08,
        jelly: 0.006,
        bounce: Math.sin(time * 0.9) * 2,
      };
    case "play":
    case "wide":
      return {
        breathe: 1 + Math.sin(time * 3.2) * 0.03,
        squash: 1 + Math.sin(time * 5) * 0.04,
        tilt: Math.sin(time * 2.2) * 0.1,
        jelly: 0.03,
        bounce: Math.sin(time * 3.2) * 6,
      };
    case "orbit":
    case "swirl":
      return {
        breathe: 1,
        squash: 1,
        tilt: time * 0.8,
        jelly: 0.02,
        bounce: 0,
      };
    case "burst":
    case "notify":
      return {
        breathe: 1 + Math.sin(time * 6) * 0.02,
        squash: 1.02,
        tilt: 0,
        jelly: 0.02,
        bounce: 0,
      };
    case "wink":
    case "alert":
    case "exclaim":
      return {
        breathe: 1.04,
        squash: 1.05,
        tilt: 0,
        jelly: 0.01,
        bounce: 0,
      };
    default:
      return {
        breathe: 1 + Math.sin(time * 1.35) * 0.02,
        squash: 1 + Math.sin(time * 1.35 + 0.4) * 0.015,
        tilt: Math.sin(time * 0.7) * 0.03,
        jelly: 0.012,
        bounce: Math.sin(time * 1.35) * 4,
      };
  }
}
