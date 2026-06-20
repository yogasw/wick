import { describe, test, expect } from "vitest";
import { buildAutoHeightSrcdoc, artifactHeightReporter } from "../richRender.js";

describe("artifact auto-height srcdoc", () => {
  test("reporter posts the given id back to the parent", () => {
    const r = artifactHeightReporter("PLAN.html");
    expect(r).toContain("wick-artifact-height");
    expect(r).toContain('"PLAN.html"');
    expect(r).toContain("postMessage");
  });

  test("buildAutoHeightSrcdoc injects the reporter before </body>", () => {
    const out = buildAutoHeightSrcdoc("<body><p>hi</p></body>", "a");
    expect(out).toContain("wick-artifact-height");
    // reporter sits just before the closing body tag, not after it
    expect(out.indexOf("wick-artifact-height")).toBeLessThan(out.lastIndexOf("</body>"));
  });

  test("carries the CSP meta from buildArtifactSrcdoc", () => {
    const out = buildAutoHeightSrcdoc("<p>hi</p>", "a");
    expect(out).toContain("Content-Security-Policy");
  });

  test("appends the reporter when there is no </body>", () => {
    const out = buildAutoHeightSrcdoc("<p>no body tag</p>", "a");
    expect(out).toContain("wick-artifact-height");
  });
});
