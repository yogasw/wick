import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import OwnerTabs from "../OwnerTabs.svelte";

describe("OwnerTabs", () => {
  test("marks the active tab and switches on click", async () => {
    const onChange = vi.fn();
    render(OwnerTabs, { props: { value: "me", onChange } });

    expect(screen.getByTestId("owner-tab-me").getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByTestId("owner-tab-all").getAttribute("aria-pressed")).toBe("false");

    await fireEvent.click(screen.getByTestId("owner-tab-all"));
    expect(onChange).toHaveBeenCalledWith("all");
  });

  test("clicking the already-active tab is a no-op (no wasted refetch)", async () => {
    const onChange = vi.fn();
    render(OwnerTabs, { props: { value: "me", onChange } });
    await fireEvent.click(screen.getByTestId("owner-tab-me"));
    expect(onChange).not.toHaveBeenCalled();
  });

  test("labels are overridable for tight spots like the untracked rail", () => {
    render(OwnerTabs, {
      props: { value: "all", onChange: vi.fn(), mineLabel: "Yours", allLabel: "All", size: "xs" },
    });
    expect(screen.getByText("Yours")).toBeDefined();
    expect(screen.getByText("All")).toBeDefined();
  });
});
