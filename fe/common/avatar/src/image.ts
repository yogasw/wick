/* Image avatars (team.Avatar kind "image"): an uploaded picture, cropped by
   numbers the server clamps (internal/agents/team/avatar_image.go). The
   picture never changes; poses animate it from the outside. */

export const AVATAR_KIND_IMAGE = "image";
export const isImageKind = (k: string | undefined | null) => k === AVATAR_KIND_IMAGE;

/** The stored crop (team.AvatarImage). v and aspect are server-owned;
    src is the versioned URL the API adds on the way out. */
export type AvatarImageSpec = { v: string; aspect: number; fit: "fit" | "fill" | string; x: number; y: number; zoom: number; src?: string };

/** What AgentAvatar draws: the crop plus where to load the picture. Any
    avatar object from the API fits (its image carries src). */
export type AvatarImageView = Partial<AvatarImageSpec>;

// Same bounds as the server (AvatarCropMaxOffset, AvatarZoomMin/Max).
export const IMAGE_OFFSET_MAX = 150;
export const IMAGE_ZOOM_MIN = 0.3;
export const IMAGE_ZOOM_MAX = 5;

const num = (v: unknown, d: number) => (typeof v === "number" && Number.isFinite(v) ? v : d);
const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/** fillZoom is the zoom at which a picture of this aspect covers the frame. */
export const fillZoom = (aspect: number) => {
  const a = num(aspect, 1) > 0 ? num(aspect, 1) : 1;
  return Math.max(a, 1 / a);
};

/** effectiveZoom: zoom 0 is automatic, Fill covers the frame, Fit shows it whole. */
export function effectiveZoom(v: Partial<AvatarImageSpec>): number {
  const z = num(v.zoom, 0);
  if (z) return clamp(z, IMAGE_ZOOM_MIN, IMAGE_ZOOM_MAX);
  return v.fit === "fit" ? 1 : fillZoom(num(v.aspect, 1));
}

export const clampOffset = (v: number) => clamp(num(v, 0), -IMAGE_OFFSET_MAX, IMAGE_OFFSET_MAX);
export const clampZoom = (v: number) => clamp(num(v, 1), IMAGE_ZOOM_MIN, IMAGE_ZOOM_MAX);

export function imageTransform(v: Partial<AvatarImageSpec>): string {
  return `translate(${clampOffset(num(v.x, 0)).toFixed(2)}%, ${clampOffset(num(v.y, 0)).toFixed(2)}%) scale(${effectiveZoom(v).toFixed(3)})`;
}

const AVATAR_PATH = /^\/(?:[A-Za-z0-9._~-]+\/)*api\/team\/agents\/[A-Za-z0-9._~%-]+\/avatar\/image(?:\?v=[0-9a-f]{16})?$/;

/** safeImageSrc lets through only the app's own avatar endpoint or a
    blob: URL this page made (a picked file's preview); anything else —
    data:, javascript:, another origin — draws nothing. */
export function safeImageSrc(src: string | undefined | null): string {
  if (!src) return "";
  if (AVATAR_PATH.test(src)) return src;
  if (typeof location !== "undefined" && src.startsWith(`blob:${location.origin}/`) && /^blob:[^\s"'<>]+$/.test(src)) return src;
  return "";
}

/** avatarImageUrl is the stored picture's URL, versioned so it can be cached. */
export const avatarImageUrl = (base: string, agentId: string, v: string) =>
  `${base}/api/team/agents/${encodeURIComponent(agentId)}/avatar/image?v=${encodeURIComponent(v)}`;
