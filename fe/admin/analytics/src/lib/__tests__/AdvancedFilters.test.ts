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
  selected: { people: ["user-b"], providers: [], models: [], match: { join: "and" as const, rules: [] } },
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
    expect(onApply).toHaveBeenCalledWith({
      people: ["user-b"],
      providers: ["codex/acct-b"],
      models: ["opus"],
      match: { join: "and", rules: [] },
    });
  });

  it("searches people and resets the draft", async () => {
    const onApply = vi.fn();
    render(AdvancedFilters, { props: props(onApply) });
    await fireEvent.input(screen.getByTestId("people-search"), { target: { value: "user a" } });
    expect(screen.queryByText("User B")).toBeNull();
    expect(screen.getByText("User A")).toBeTruthy();
    await fireEvent.click(screen.getByText("Reset"));
    await fireEvent.click(screen.getByText("Apply"));
    expect(onApply).toHaveBeenCalledWith({ people: [], providers: [], models: [], match: { join: "and", rules: [] } });
  });

  it("builds conditions at the bottom, joined by AND or OR", async () => {
    const onApply = vi.fn();
    render(AdvancedFilters, { props: props(onApply) });
    await fireEvent.click(screen.getByTestId("cond-add"));
    await fireEvent.input(screen.getAllByTestId("cond-value")[0], { target: { value: "daily-2026-10" } });
    await fireEvent.click(screen.getByTestId("cond-add"));
    await fireEvent.change(screen.getAllByTestId("cond-field")[1], { target: { value: "model" } });
    await fireEvent.change(screen.getAllByTestId("cond-op")[1], { target: { value: "not_exact" } });
    await fireEvent.input(screen.getAllByTestId("cond-value")[1], { target: { value: "haiku" } });
    await fireEvent.click(screen.getByText("Match any (OR)"));
    await fireEvent.click(screen.getByText("Apply"));
    expect(onApply).toHaveBeenCalledWith(
      expect.objectContaining({
        match: {
          join: "or",
          rules: [
            { field: "title", op: "contains", value: "daily-2026-10" },
            { field: "model", op: "not_exact", value: "haiku" },
          ],
        },
      }),
    );
  });

  it("removes a condition and flags a regex that does not compile", async () => {
    render(AdvancedFilters, { props: props() });
    await fireEvent.click(screen.getByTestId("cond-add"));
    await fireEvent.change(screen.getByTestId("cond-op"), { target: { value: "regex" } });
    await fireEvent.input(screen.getByTestId("cond-value"), { target: { value: "daily-(" } });
    expect(screen.getByTestId("cond-regex-error")).toBeTruthy();
    await fireEvent.click(screen.getByLabelText("Remove condition"));
    expect(screen.queryAllByTestId("cond-row")).toHaveLength(0);
  });
});
