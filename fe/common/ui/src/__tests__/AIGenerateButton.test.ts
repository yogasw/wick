import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import AIGenerateButton from "../AIGenerateButton.svelte";
import type { AIGenAPI, AIGenJob } from "../aigen.js";

function apiWith(final: AIGenJob<string>, opts: { hold?: boolean } = {}) {
  const api: AIGenAPI<string> = {
    submit: vi.fn(async () => ({ id: "gen_1", kind: "k", status: "queued", position: 2 }) as AIGenJob<string>),
    get: vi.fn(async () => (opts.hold ? ({ id: "gen_1", kind: "k", status: "working" } as AIGenJob<string>) : final)),
    cancel: vi.fn(async () => ({ id: "gen_1", kind: "k", status: "canceled" }) as AIGenJob<string>),
  };
  return api;
}

describe("AIGenerateButton", () => {
  test("shows the queue position, then a draft with Use / Discard", async () => {
    const api = apiWith({ id: "gen_1", kind: "k", status: "done", result: "You are a careful reviewer." });
    const onUse = vi.fn();
    render(AIGenerateButton, {
      props: { kind: "k", input: () => ({ text: "reviewer" }), onUse, api, pollMs: 10, current: "Old prompt" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "✨ Generate" }));
    expect((await screen.findByRole("status")).textContent).toContain("Queued · #2");
    expect(await screen.findByText("You are a careful reviewer.")).toBeTruthy();
    expect(screen.getByText("Old prompt")).toBeTruthy();
    expect(onUse).not.toHaveBeenCalled(); // never overwrites silently
    await fireEvent.click(screen.getByRole("button", { name: "Use" }));
    expect(onUse).toHaveBeenCalledWith("You are a careful reviewer.");
    expect(screen.queryByTestId("ai-generate-draft")).toBeNull();
  });

  test("Discard drops the draft without using it", async () => {
    const api = apiWith({ id: "gen_1", kind: "k", status: "done", result: "draft" });
    const onUse = vi.fn();
    render(AIGenerateButton, { props: { kind: "k", input: () => ({ text: "x" }), onUse, api, pollMs: 10 } });
    await fireEvent.click(screen.getByRole("button", { name: "✨ Generate" }));
    await fireEvent.click(await screen.findByRole("button", { name: "Discard" }));
    expect(onUse).not.toHaveBeenCalled();
    expect(screen.queryByTestId("ai-generate-draft")).toBeNull();
  });

  test("shows Writing… and Cancel stops the job", async () => {
    const api = apiWith({ id: "gen_1", kind: "k", status: "done" }, { hold: true });
    render(AIGenerateButton, { props: { kind: "k", input: () => ({ text: "x" }), onUse: vi.fn(), api, pollMs: 10 } });
    await fireEvent.click(screen.getByRole("button", { name: "✨ Generate" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("Writing…"));
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(api.cancel).toHaveBeenCalledWith("gen_1");
    expect(await screen.findByRole("button", { name: "✨ Generate" })).toBeTruthy();
  });

  test("a failure shows the error and offers Retry", async () => {
    const api = apiWith({ id: "gen_1", kind: "k", status: "failed", error: "no free agent slot" });
    render(AIGenerateButton, { props: { kind: "k", input: () => ({ text: "x" }), onUse: vi.fn(), api, pollMs: 10 } });
    await fireEvent.click(screen.getByRole("button", { name: "✨ Generate" }));
    expect((await screen.findByRole("alert")).textContent).toContain("no free agent slot");
    await fireEvent.click(screen.getByRole("button", { name: "↻ Retry" }));
    expect(api.submit).toHaveBeenCalledTimes(2);
  });

  test("validate refuses the click without submitting", async () => {
    const api = apiWith({ id: "gen_1", kind: "k", status: "done", result: "x" });
    render(AIGenerateButton, {
      props: { kind: "k", input: () => ({ text: "" }), validate: () => "Describe the agent first.", onUse: vi.fn(), api },
    });
    await fireEvent.click(screen.getByRole("button", { name: "✨ Generate" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Describe the agent first.");
    expect(api.submit).not.toHaveBeenCalled();
  });

  test("autoUse hands the result straight over", async () => {
    const api = apiWith({ id: "gen_1", kind: "k", status: "done", result: "draft" });
    const onUse = vi.fn();
    render(AIGenerateButton, {
      props: { kind: "k", input: () => ({ text: "x" }), onUse, api, pollMs: 10, autoUse: true, label: "Parse →" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Parse →" }));
    await waitFor(() => expect(onUse).toHaveBeenCalledWith("draft"));
    expect(screen.queryByTestId("ai-generate-draft")).toBeNull();
  });
});
