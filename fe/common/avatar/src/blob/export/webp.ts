import { createRuntime } from "../core/runtime";
import { drawFrame } from "../render/canvas";
import type { BlobSnapshot } from "../core/types";
import type { ExportOptions } from "./options";

export async function exportWebp(
  snapshot: BlobSnapshot,
  options: ExportOptions = {},
): Promise<Blob> {
  const size = options.size ?? 512;
  const time = 0;
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("Canvas 2D is not available");

  const runtime = createRuntime(snapshot);
  const visual = runtime.step(snapshot, 1, time);
  if (options.background) {
    ctx.fillStyle = options.background;
    ctx.fillRect(0, 0, size, size);
  }
  drawFrame(ctx, visual, size, time);

  return await new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) => {
        if (!blob) reject(new Error("WebP export failed"));
        else resolve(blob);
      },
      "image/webp",
      0.92,
    );
  });
}
