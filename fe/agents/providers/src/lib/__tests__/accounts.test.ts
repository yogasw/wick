import { describe, expect, it } from "vitest";
import { suggestName, defaultOMPProfile, validOMPProfile, accountStorePreview, typeLabel, typeOption } from "../accounts";

describe("accounts helpers", () => {
  it("suggests the first free name", () => {
    expect(suggestName("omp", [])).toBe("omp");
    expect(suggestName("omp", ["omp"])).toBe("omp_2");
    expect(suggestName("opencode", ["opencode", "opencode_2"])).toBe("opencode_3");
  });
  it("mirrors the BE default omp profile", () => {
    expect(defaultOMPProfile("omp")).toBe("wick-omp");
    expect(defaultOMPProfile("My Omp_2")).toBe("wick-my-omp_2");
    expect(defaultOMPProfile("  ")).toBe("wick-default");
    expect(validOMPProfile(defaultOMPProfile("x".repeat(90)))).toBe(true);
    expect(validOMPProfile("Bad Name")).toBe(false);
  });
  it("previews the account store", () => {
    expect(accountStorePreview("omp", "work")).toBe("profile wick-work");
    expect(accountStorePreview("opencode", "oc_2")).toContain("providers/opencode/oc_2");
    expect(accountStorePreview("claude", "x")).toBe("");
  });
  it("labels new types", () => {
    expect(typeLabel("omp")).toContain("oh-my-pi");
    expect(typeLabel("unknown")).toBe("unknown");
    expect(typeOption("omp")).toEqual({ label: "oh-my-pi (omp)", value: "omp", description: "Login ChatGPT atau Claude · 1 profile per instance" });
    expect(typeOption("gemini").badge).toBe("experimental");
    expect(typeOption("x")).toEqual({ label: "x", value: "x" });
  });
});
