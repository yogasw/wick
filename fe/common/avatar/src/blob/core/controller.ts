import {
  ONE_SHOT_STATES,
  type BlobExpression,
  type BlobMascotOptions,
  type BlobShape,
  type BlobSnapshot,
  type BlobState,
  type Gaze,
} from "./types";

export type BlobMascotController = {
  getSnapshot(): BlobSnapshot;
  subscribe(listener: () => void): () => void;
  setShape(shape: BlobShape): void;
  setExpression(expression: BlobExpression): void;
  setState(state: BlobState): void;
  setColor(color: string): void;
  lookAt(gaze: Partial<Gaze>): void;
  resetGaze(): void;
  poke(): void;
};

export function createController(
  options: BlobMascotOptions = {},
): BlobMascotController {
  let snapshot: BlobSnapshot = {
    shape: options.shape ?? "circle",
    expression: options.expression ?? "neutral",
    state: options.state ?? "idle",
    color: options.color ?? "#111111",
    gaze: { yaw: 0, pitch: 0 },
  };

  const listeners = new Set<() => void>();

  function emit() {
    for (const listener of listeners) listener();
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    setShape(shape) {
      snapshot = { ...snapshot, shape };
      emit();
    },
    setExpression(expression) {
      snapshot = { ...snapshot, expression };
      emit();
    },
    setState(state) {
      snapshot = { ...snapshot, state };
      emit();
      if (ONE_SHOT_STATES.includes(state)) {
        window.setTimeout(() => {
          if (snapshot.state === state) {
            snapshot = { ...snapshot, state: "idle" };
            emit();
          }
        }, 900);
      }
    },
    setColor(color) {
      snapshot = { ...snapshot, color };
      emit();
    },
    lookAt(gaze) {
      const next = {
        yaw: gaze.yaw ?? snapshot.gaze.yaw,
        pitch: gaze.pitch ?? snapshot.gaze.pitch,
      };
      if (next.yaw === snapshot.gaze.yaw && next.pitch === snapshot.gaze.pitch) return;
      snapshot = { ...snapshot, gaze: next };
      emit();
    },
    resetGaze() {
      if (snapshot.gaze.yaw === 0 && snapshot.gaze.pitch === 0) return;
      snapshot = { ...snapshot, gaze: { yaw: 0, pitch: 0 } };
      emit();
    },
    poke() {
      snapshot = { ...snapshot, state: "burst" };
      emit();
      window.setTimeout(() => {
        if (snapshot.state === "burst") {
          snapshot = { ...snapshot, state: "idle" };
          emit();
        }
      }, 900);
    },
  };
}
