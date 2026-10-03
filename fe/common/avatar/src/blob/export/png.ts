import { drawBlob } from "../render/canvas";
import type { BlobSnapshot } from "../core/types";

export async function exportPng(
  snapshot: BlobSnapshot,
  size = 512,
): Promise<Blob> {
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("Canvas 2D is not available");
  drawBlob(ctx, snapshot, size, 0);
  return await new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (!blob) reject(new Error("PNG export failed"));
      else resolve(blob);
    }, "image/png");
  });
}
