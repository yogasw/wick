import { describe, test, expect } from "vitest";
import { ticketsSubtitle, withProject, projectCount } from "../ticketProjects.js";

describe("ticketProjects", () => {
  test("header line names the projects, clips long names, folds the rest into +N", () => {
    expect(ticketsSubtitle([], {})).toBe("No projects selected");
    expect(ticketsSubtitle(["a", "b"], { a: "Alpha", b: "Beta" })).toBe("📁 Alpha · 📁 Beta · each ticket stays in its project");
    const long = "x".repeat(30);
    expect(ticketsSubtitle(["a"], { a: long })).toBe(`📁 ${"x".repeat(23)}… · each ticket stays in its project`);
    expect(ticketsSubtitle(["a", "b", "c", "d", "e"], { a: "A", b: "B", c: "C" })).toBe("📁 A · 📁 B · 📁 C +2 · each ticket stays in its project");
  });
  test("withProject keeps the order picked; projectCount", () => {
    expect(withProject(["a", "b"], "a", true)).toEqual(["b", "a"]);
    expect(withProject(["a", "b"], "a", false)).toEqual(["b"]);
    expect(projectCount(1)).toBe("1 project");
    expect(projectCount(0)).toBe("");
  });
});
