import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SaveStatus from "../components/SaveStatus.svelte";

describe("SaveStatus", () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  test("a fast save never paints 'Saving…' — the whole point of the delay", async () => {
    const { rerender, container } = render(SaveStatus, {
      props: { status: { state: "saving" as const }, onRetry: vi.fn() },
    });
    // 200ms in: still under the 400ms threshold, nothing shown yet.
    await vi.advanceTimersByTimeAsync(200);
    expect(container.textContent).not.toContain("Saving");

    // Request completes before the threshold: it must stay unpainted.
    await rerender({ status: { state: "saved" as const, savedAt: Date.now() }, onRetry: vi.fn() });
    await vi.advanceTimersByTimeAsync(500);
    expect(container.textContent).not.toContain("Saving");
    expect(container.textContent).toContain("Saved");
  });

  test("a slow save does show 'Saving…' once past the threshold", async () => {
    const { container } = render(SaveStatus, {
      props: { status: { state: "saving" as const }, onRetry: vi.fn() },
    });
    await vi.advanceTimersByTimeAsync(500);
    expect(container.textContent).toContain("Saving");
  });

  test("idle with no prior save renders nothing", () => {
    const { container } = render(SaveStatus, {
      props: { status: { state: "idle" as const }, onRetry: vi.fn() },
    });
    expect(container.querySelector("[data-testid=save-status]")?.textContent?.trim()).toBe("");
  });

  test("error shows a retry button that calls back", async () => {
    const onRetry = vi.fn();
    render(SaveStatus, {
      props: { status: { state: "error" as const, message: "boom" }, onRetry },
    });
    expect(screen.getByText("Not saved")).toBeTruthy();
    await fireEvent.click(screen.getByText("Retry"));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
