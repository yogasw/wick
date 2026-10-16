import { describe, test, expect } from "vitest";
import { effectiveZoom, imageTransform, safeImageSrc, switchAvatarKind } from "../index.js";

describe("image avatar helpers", () => {
  test("safeImageSrc allows only the avatar endpoint or a same-origin blob:", () => {
    expect(safeImageSrc("/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef")).not.toBe("");
    expect(safeImageSrc(`blob:${location.origin}/1234-abcd`)).not.toBe("");
    for (const bad of ["data:image/svg+xml,<svg onload=alert(1)>", "javascript:alert(1)", "https://evil.example/x.png", "//evil.example/api/team/agents/a/avatar/image", "blob:https://evil.example/1", "/tools/agents/api/team/agents/a1/avatar/image?v=x\" onerror=\"alert(1)"]) {
      expect(safeImageSrc(bad)).toBe("");
    }
  });

  test("zoom: automatic fill/fit and clamping", () => {
    expect(effectiveZoom({ fit: "fill", aspect: 2, zoom: 0 })).toBe(2);
    expect(effectiveZoom({ fit: "fit", aspect: 2, zoom: 0 })).toBe(1);
    expect(effectiveZoom({ zoom: 99 })).toBe(5);
    expect(imageTransform({ x: 999, y: -999, zoom: 1 })).toBe("translate(150.00%, -150.00%) scale(1.000)");
  });

  test("switchAvatarKind keeps the picture's crop across kinds (Classic and back restores it)", () => {
    const img = { v: "0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0 };
    const classic = switchAvatarKind({ kind: "image", shape: "circle", color: "#111111", image: img }, "");
    expect(classic).toEqual({ shape: "circle", color: "#111111", image: img });
    expect(switchAvatarKind(classic, "image")).toEqual({ kind: "image", shape: "circle", color: "#111111", image: img });
    expect(switchAvatarKind({ shape: "circle", color: "#111111" }, "image")).toEqual({ kind: "image", shape: "circle", color: "#111111" });
  });
});
