import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import AdvancedFilters from "$lib/AdvancedFilters.svelte";

const props = (onApply = vi.fn()) => ({
  open: true,
  people: [
    { id: "user-a", name: "User A" },
    { id: "user-b", name: "User B" },
    { id: "(unattributed)", name: "Unattributed / machine" },
  ],
  providers: ["claude/acct-a", "codex/acct-b"],
  models: ["(none)", "opus"],
  selected: { people: ["user-b"], providers: [], models: [] },
  onApply,
  onClose: vi.fn(),
});

describe("AdvancedFilters", () => {
  it("applies the draft only on Apply", async () => {
    const onApply = vi.fn();
    render(AdvancedFilters, { props: props(onApply) });
    await fireEvent.click(screen.getByText("codex/acct-b"));
    await fireEvent.click(screen.getByText("opus"));
    expect(onApply).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByText("Apply"));
    expect(onApply).toHaveBeenCalledWith({ people: ["user-b"], providers: ["codex/acct-b"], models: ["opus"] });
  });

  it("searches people and resets the draft", async () => {
    const onApply = vi.fn();
    render(AdvancedFilters, { props: props(onApply) });
    await fireEvent.input(screen.getByTestId("people-search"), { target: { value: "user a" } });
    expect(screen.queryByText("User B")).toBeNull();
    expect(screen.getByText("User A")).toBeTruthy();
    await fireEvent.click(screen.getByText("Reset"));
    await fireEvent.click(screen.getByText("Apply"));
    expect(onApply).toHaveBeenCalledWith({ people: [], providers: [], models: [] });
  });
});
