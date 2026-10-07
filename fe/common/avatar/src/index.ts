/* The Team app's living avatar, shared by every bundle that draws an agent
   (conversation roster/settings/wizard, overview card). An avatar is
   classic (shape.ts) or a blob mascot (blob.ts, vendored core in blob/). Geometry lives in
   shape.ts, the shared animation loop in ticker.ts. */
export { default as AgentAvatar } from "./AgentAvatar.svelte";
export { default as BlobAvatar } from "./BlobAvatar.svelte";
export { default as BlobAvatarPicker } from "./BlobAvatarPicker.svelte";
export {
  AVATAR_KIND_BLOB, isBlobKind, BLOB_SHAPES, BLOB_EXPRESSIONS, BLOB_COLORS, BLOB_GAZE_MIN,
  normalizeBlobShape, normalizeBlobExpression, switchAvatarKind, type AvatarSpec, blobStateFor, randomBlob, blobColor,
  type AvatarKind, type BlobLook, type BlobShape, type BlobExpression, type BlobState,
} from "./blob.js";
export * from "./shape.js";
export { subscribe, pointer, pointerActive, prefersReducedMotion, POINTER_IDLE_MS } from "./ticker.js";
export { idleAnimationsOn, setIdleAnimations } from "./idle.js";
export {
  createFidget, createFidgetSlots, seededRand, FIDGET_MAX_ACTIVE, FIDGET_DROWSY_AFTER, FIDGET_BIG_MIN,
  type Fidget, type FidgetEnv, type FidgetPose,
} from "./blob/motion/fidget";
export {
  AVATAR_EVENTS, AVATAR_EVENT_LABELS, DEFAULT_EVENT_POSES, isAvatarEvent, toolEvent, toolWords, eventOf, poseFor,
  fallbackState, resolvePose, cleanOverrides, type AvatarEvent, type EventPose, type EventOverride, type EventOverrides, type PoseInput, type ResolvedPose,
} from "./events.js";
export { STATES as BLOB_STATES } from "./blob/core/types";
