/* The Team app's living avatar, shared by every bundle that draws an agent
   (conversation roster/settings/wizard, overview card). Geometry lives in
   shape.ts, the shared animation loop in ticker.ts. */
export { default as AgentAvatar } from "./AgentAvatar.svelte";
export * from "./shape.js";
export { subscribe, pointer, pointerActive, prefersReducedMotion, POINTER_IDLE_MS } from "./ticker.js";
