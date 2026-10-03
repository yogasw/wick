import { describe, test, expect, beforeEach, afterEach, vi } from "vitest";
import {
  BLOB_COLORS, BLOB_EXPRESSIONS, BLOB_SHAPES, blobAnimates, blobColor, blobFollowsGaze, blobStateFor,
  clearStillCache, gazeFromOffset, isBlobKind, normalizeBlobExpression, normalizeBlobShape, randomBlob,
  stillCacheSize, stillKey, stillUrl,
} from "../blob.js";
import { AVATAR_STATES } from "../shape.js";
import { stubCanvas } from "./canvasStub.js";

const look = { shape: "cloud", expression: "happy", color: "#4b8fea" } as const;

describe("blob helpers", () => {
  test("sets match the server's team.BlobShapes / BlobExpressions", () => {
    expect([...BLOB_SHAPES]).toEqual(["circle", "pebble", "squircle", "capsule", "triangle", "cloud", "droplet", "flame", "medal", "acorn", "jellyfish", "clover"]);
    expect(BLOB_EXPRESSIONS).toHaveLength(12);
    expect(BLOB_EXPRESSIONS[0]).toBe("neutral");
    expect(BLOB_COLORS.every((c) => c === c.toLowerCase())).toBe(true);
  });

  test("kind and normalizers", () => {
    expect(isBlobKind("blob")).toBe(true);
    expect(isBlobKind("")).toBe(false);
    expect(isBlobKind(undefined)).toBe(false);
    expect(normalizeBlobShape("diamond")).toBe("circle");
    expect(normalizeBlobShape("acorn")).toBe("acorn");
    expect(normalizeBlobExpression("smug")).toBe("neutral");
    expect(blobColor("#ABC")).toBe("#abc");
    expect(blobColor("red")).toBe(BLOB_COLORS[8]);
  });

  test("every roster state maps to a blob motion; egg breathes as idle", () => {
    for (const s of AVATAR_STATES) expect(typeof blobStateFor(s)).toBe("string");
    expect(blobStateFor("thinking")).toBe("thinking");
    expect(blobStateFor("orbit")).toBe("orbit");
    expect(blobStateFor("sleep")).toBe("sleep");
    expect(blobStateFor("egg")).toBe("idle");
  });

  test("only live, non-reduced avatars animate", () => {
    expect(blobAnimates({ live: true })).toBe(true);
    expect(blobAnimates({})).toBe(false);
    expect(blobAnimates({ live: true, reduced: true })).toBe(false);
    expect(blobAnimates({ live: true, still: true })).toBe(false);
  });

  test("gaze only at large sizes and never asleep", () => {
    expect(blobFollowsGaze(72, "idle")).toBe(true);
    expect(blobFollowsGaze(36, "idle")).toBe(false);
    expect(blobFollowsGaze(72, "sleep")).toBe(false);
  });

  test("gazeFromOffset clamps to the package's ±36 yaw / ±28 pitch", () => {
    expect(gazeFromOffset(0, 0, 1000, 800)).toEqual({ yaw: 0, pitch: -0 });
    expect(gazeFromOffset(5000, 5000, 1000, 800)).toEqual({ yaw: 36, pitch: -28 });
    expect(gazeFromOffset(-5000, -5000, 1000, 800)).toEqual({ yaw: -36, pitch: 28 });
  });

  test("randomBlob picks from the sets, edge-safe at rand → 1", () => {
    expect(randomBlob(() => 0)).toEqual({ shape: "circle", expression: "neutral", color: BLOB_COLORS[0] });
    const top = randomBlob(() => 0.9999999);
    expect(top.shape).toBe("clover");
    expect(top.expression).toBe("unimpressed");
    expect(randomBlob(() => 1).shape).toBe("clover");
  });
});

describe("stillUrl cache", () => {
  beforeEach(() => clearStillCache());
  afterEach(() => vi.restoreAllMocks());

  test("no canvas 2D → empty, nothing cached", () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    expect(stillUrl(look, "idle", 40)).toBe("");
    expect(stillCacheSize()).toBe(0);
  });

  test("draws a look once, then serves it from cache", () => {
    const { getContext, toDataURL } = stubCanvas();
    expect(stillUrl(look, "idle", 40)).toBe("data:image/png;base64,STILL");
    expect(stillUrl(look, "idle", 40)).toBe("data:image/png;base64,STILL");
    expect(getContext).toHaveBeenCalledTimes(1);
    expect(toDataURL).toHaveBeenCalledTimes(1);
    stillUrl(look, "thinking", 40);
    stillUrl(look, "idle", 80);
    expect(getContext).toHaveBeenCalledTimes(3);
    expect(stillCacheSize()).toBe(3);
    expect(stillKey(look, "idle", 40)).toBe("cloud|happy|#4b8fea|idle|40");
  });

  test("the cache is bounded", () => {
    stubCanvas();
    for (let i = 0; i < 300; i++) stillUrl(look, "idle", 10 + i);
    expect(stillCacheSize()).toBe(256);
  });

  test("every shape × expression draws without throwing", () => {
    stubCanvas();
    for (const shape of BLOB_SHAPES) for (const expression of BLOB_EXPRESSIONS) expect(stillUrl({ shape, expression, color: "#111111" }, "thinking", 32)).not.toBe("");
  });
});
