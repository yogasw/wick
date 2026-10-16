import { describe, test, expect } from "vitest";
import { render } from "@testing-library/svelte";
import TeammatesChip from "../TeammatesChip.svelte";

describe("TeammatesChip (Team app header + conversation header)", () => {
  test("reads working · waiting on N teammates", () => {
    const { container } = render(TeammatesChip, { props: { count: 2 } });
    expect(container.querySelector("[data-teammate-badge]")?.textContent).toContain("working · waiting on 2 teammates");
  });
  test("renders nothing at 0", () => {
    const { container } = render(TeammatesChip, { props: { count: 0 } });
    expect(container.querySelector("[data-teammate-badge]")).toBeNull();
  });
});
