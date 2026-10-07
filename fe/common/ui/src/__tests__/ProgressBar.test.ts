import { describe, it, expect } from "vitest";
import { render } from "@testing-library/svelte";
import ProgressBar from "../ProgressBar.svelte";

describe("ProgressBar", () => {
  it("fills to pct and shows the label", () => {
    const { getByRole, getByText } = render(ProgressBar, { props: { pct: 45, label: "Downloading 45%" } });
    const bar = getByRole("progressbar");
    expect(bar.getAttribute("aria-valuenow")).toBe("45");
    expect((bar.firstElementChild as HTMLElement).style.width).toBe("45%");
    expect(getByText("Downloading 45%")).toBeTruthy();
  });

  it("clamps out-of-range values", () => {
    const { getByRole } = render(ProgressBar, { props: { pct: 180 } });
    expect((getByRole("progressbar").firstElementChild as HTMLElement).style.width).toBe("100%");
  });

  it("negative pct is indeterminate", () => {
    const { getByRole } = render(ProgressBar, { props: { pct: -1, testid: "pb" } });
    const bar = getByRole("progressbar");
    expect(bar.hasAttribute("aria-valuenow")).toBe(false);
    expect(bar.firstElementChild?.className).toContain("animate-pulse");
  });
});
