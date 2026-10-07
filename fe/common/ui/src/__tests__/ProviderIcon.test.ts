import { describe, it, expect } from "vitest";
import { render } from "@testing-library/svelte";
import ProviderIcon from "../ProviderIcon.svelte";
import { providerBrand } from "../provider-brand";

describe("providerBrand", () => {
  it("maps types and type/name values to a brand", () => {
    expect(providerBrand("omp/yoga")).toBe("omp");
    expect(providerBrand("opencode")).toBe("opencode");
    expect(providerBrand("claude/engineer")).toBe("claude");
    expect(providerBrand("codex/codex")).toBe("codex");
    expect(providerBrand("gemini")).toBe("gemini");
    expect(providerBrand("wick")).toBe("wick");
    expect(providerBrand("something")).toBe("other");
  });
});

describe("ProviderIcon", () => {
  it("renders the brand mark for a known type", () => {
    const { getByTestId } = render(ProviderIcon, { props: { value: "omp" } });
    const el = getByTestId("provider-icon");
    expect(el.getAttribute("data-brand")).toBe("omp");
    expect(el.getAttribute("src")).toBe("/public/img/providers/omp.svg");
  });
  it("falls back to a generic glyph for an unknown type", () => {
    const { getByTestId } = render(ProviderIcon, { props: { value: "mystery" } });
    expect(getByTestId("provider-icon").getAttribute("data-brand")).toBe("other");
  });
});
