/* The Team app's living avatar, shared by every bundle that draws an agent
   (conversation roster/settings/wizard, overview card). An avatar is
   classic (shape.ts) or a blob mascot (blob.ts, vendored core in blob/). Geometry lives in
   shape.ts, the shared animation loop in ticker.ts. */
export { default as AgentAvatar } from "./AgentAvatar.svelte";
export { default as BlobAvatar } from "./BlobAvatar.svelte";
export { default as BlobAvatarPicker } from "./BlobAvatarPicker.svelte";
export {
  AVATAR_KIND_BLOB, isBlobKind, BLOB_SHAPES, BLOB_EXPRESSIONS, BLOB_COLORS, BLOB_GAZE_MIN,
  normalizeBlobShape, normalizeBlobExpression, blobStateFor, randomBlob, blobColor,
  type AvatarKind, type BlobLook, type BlobShape, type BlobExpression, type BlobState,
} from "./blob.js";
export * from "./shape.js";
export { subscribe, pointer, pointerActive, prefersReducedMotion, POINTER_IDLE_MS } from "./ticker.js";
