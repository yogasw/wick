/* Image avatars on the client: reading a picked file for the upload and
   the starting crop. The server is the gate (type by magic bytes, size,
   dimensions, re-encode); this only avoids sending what it would refuse. */
import type { AgentAvatarSpec } from "./api/team.js";

export const AVATAR_IMAGE_MAX_BYTES = 2 << 20;
export const AVATAR_IMAGE_TYPES = ["image/png", "image/jpeg", "image/webp"];

export type ImageCrop = { fit: string; x: number; y: number; zoom: number };
export const DEFAULT_CROP: ImageCrop = { fit: "fill", x: 0, y: 0, zoom: 0 };

/** pickError is why a picked file cannot be sent, "" when it can. */
export function pickError(f: File): string {
  if (!AVATAR_IMAGE_TYPES.includes(f.type)) return "Use a PNG, JPEG or WebP image.";
  if (f.size > AVATAR_IMAGE_MAX_BYTES) return "That image is larger than 2 MB.";
  return "";
}

const toBase64 = (buf: ArrayBuffer) => {
  const b = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000));
  return btoa(s);
};

/** fileData is the upload body's base64. WebP is redrawn as PNG first
    (the server decodes PNG and JPEG only); a browser that cannot draw it
    sends it as is and gets the server's message. */
export async function fileData(f: File): Promise<string> {
  if (f.type === "image/webp" && typeof createImageBitmap === "function" && typeof OffscreenCanvas === "function") {
    try {
      const bmp = await createImageBitmap(f);
      const c = new OffscreenCanvas(bmp.width, bmp.height);
      c.getContext("2d")?.drawImage(bmp, 0, 0);
      return toBase64(await (await c.convertToBlob({ type: "image/png" })).arrayBuffer());
    } catch {
      /* fall through */
    }
  }
  return toBase64(await f.arrayBuffer());
}

/** imageAvatar is the spec saved once a picture is stored. */
export const imageAvatar = (a: AgentAvatarSpec, v: string, aspect: number, crop: ImageCrop = DEFAULT_CROP): AgentAvatarSpec => ({
  kind: "image",
  shape: a.shape,
  color: a.color,
  image: { v, aspect, ...crop },
  ...(a.events ? { events: a.events } : {}),
});
