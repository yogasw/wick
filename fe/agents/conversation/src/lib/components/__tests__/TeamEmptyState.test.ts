import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

import TeamEmptyState from "../TeamEmptyState.svelte";

describe("TeamEmptyState", () => {
  test("explains the Team and the Captain role", () => {
    render(TeamEmptyState, { props: { onCreate: vi.fn(), onRemote: vi.fn() } });
    expect(screen.getByRole("heading", { name: "Build your Team" })).toBeDefined();
    const steps = screen.getByTestId("team-empty-steps").textContent ?? "";
    expect(steps).toContain("Your first agent becomes the Captain");
    expect(steps).toContain("The role can move");
    expect(steps).toContain("You pick the provider & model");
    expect(screen.getByTestId("team-empty").textContent).toContain("cannot be the Captain");
  });

  test("the buttons open the wizard and the remote wizard", async () => {
    const onCreate = vi.fn();
    const onRemote = vi.fn();
    render(TeamEmptyState, { props: { onCreate, onRemote } });
    await fireEvent.click(screen.getByTestId("team-empty-create"));
    expect(onCreate).toHaveBeenCalledOnce();
    await fireEvent.click(screen.getByTestId("team-empty-remote"));
    expect(onRemote).toHaveBeenCalledOnce();
  });

  test("with no candidates it is the new-user page", () => {
    render(TeamEmptyState, { props: { onCreate: vi.fn(), onRemote: vi.fn(), candidates: [] } });
    expect(screen.getByRole("heading", { name: "Build your Team" })).toBeDefined();
    expect(screen.queryByTestId("team-empty-candidates")).toBeNull();
  });

  test("after the Captain is gone it offers the user's own agents", async () => {
    const onMakeCaptain = vi.fn();
    const c = { id: "a1", name: "Helper", handle: "helper" };
    render(TeamEmptyState, { props: { onCreate: vi.fn(), onRemote: vi.fn(), candidates: [c], onMakeCaptain } });
    expect(screen.getByRole("heading", { name: "Your Team needs a Captain" })).toBeDefined();
    await fireEvent.click(screen.getByRole("button", { name: /Helper/ }));
    expect(onMakeCaptain).toHaveBeenCalledWith(c);
  });
});
