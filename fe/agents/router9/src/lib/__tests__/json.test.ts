import { describe, it, expect } from "vitest";
import { prettyJSON } from "../json.js";

describe("prettyJSON", () => {
  it("pretty-prints valid JSON with 2-space indent", () => {
    expect(prettyJSON('{"a":1,"b":[2,3]}')).toBe('{\n  "a": 1,\n  "b": [\n    2,\n    3\n  ]\n}');
  });

  it("returns non-JSON unchanged", () => {
    expect(prettyJSON("event: message\ndata: hi")).toBe("event: message\ndata: hi");
  });

  it("returns empty for blank input", () => {
    expect(prettyJSON("")).toBe("");
    expect(prettyJSON("   ")).toBe("");
  });
});
