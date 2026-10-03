import type { BlobState } from "../core/types";

export function hasParticles(state: BlobState): boolean {
  return state === "burst" || state === "comet" || state === "notify" || state === "orbit";
}
