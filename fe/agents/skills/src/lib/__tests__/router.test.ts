import { describe, it, expect } from "vitest";
import { match } from "../router.js";

describe("router.match", () => {
  it("returns params for /skills/:name", () => {
    expect(match("/skills/:name", "/skills/my-skill")).toEqual({ name: "my-skill" });
  });

  it("returns null for non-matching pattern", () => {
    expect(match("/skills/:name", "/")).toBeNull();
    expect(match("/skills/:name", "/skills/foo/files/bar")).toBeNull();
  });

  it("decodes URI-encoded params", () => {
    expect(match("/skills/:name", "/skills/my%20skill")).toEqual({ name: "my skill" });
  });

  it("matches file route with 4 segments", () => {
    expect(match("/skills/:folder/files/:file", "/skills/myfolder/files/myfile.md")).toEqual({
      folder: "myfolder",
      file: "myfile.md",
    });
  });

  it("returns null when file pattern given list path", () => {
    expect(match("/skills/:folder/files/:file", "/skills/foo")).toBeNull();
  });

  it("returns empty object for exact root match", () => {
    expect(match("/", "/")).toEqual({});
  });
});
