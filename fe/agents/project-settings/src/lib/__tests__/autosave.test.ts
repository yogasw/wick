import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { createAutosave } from "../autosave.js";

describe("createAutosave", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  test("debounces rapid changes into a single save", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const a = createAutosave({ save, debounceMs: 800 });

    a.schedule();
    vi.advanceTimersByTime(300);
    a.schedule();
    vi.advanceTimersByTime(300);
    a.schedule();
    expect(save).not.toHaveBeenCalled();

    vi.advanceTimersByTime(800);
    await vi.runAllTimersAsync();
    expect(save).toHaveBeenCalledTimes(1);
  });

  test("flush saves immediately without waiting for the debounce", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const a = createAutosave({ save, debounceMs: 800 });

    a.schedule();
    a.flush();
    await vi.runAllTimersAsync();
    expect(save).toHaveBeenCalledTimes(1);
  });

  // flush() means "commit the pending edit now", not "save unconditionally":
  // tabbing through an untouched form must not write anything.
  test("flush with nothing pending does not save", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const a = createAutosave({ save, debounceMs: 800 });

    a.flush();
    await vi.runAllTimersAsync();
    expect(save).not.toHaveBeenCalled();
  });

  test("coalesces changes made while a save is in flight into one follow-up", async () => {
    let release: (() => void) | undefined;
    const save = vi.fn().mockImplementation(
      () => new Promise<void>((res) => { release = res; }),
    );
    const a = createAutosave({ save, debounceMs: 800 });

    a.schedule();
    a.flush();
    await Promise.resolve();
    expect(save).toHaveBeenCalledTimes(1);

    // Two more edits while the first request is still open.
    a.schedule();
    a.schedule();
    vi.advanceTimersByTime(2000);
    expect(save).toHaveBeenCalledTimes(1); // still queued behind the in-flight one

    release!();
    await vi.runAllTimersAsync();
    expect(save).toHaveBeenCalledTimes(2); // exactly one follow-up, not two
  });

  test("status goes saving -> saved and reports the save time", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const seen: string[] = [];
    const a = createAutosave({
      save,
      debounceMs: 800,
      onStatus: (s) => seen.push(s.state),
    });

    a.schedule();
    a.flush();
    await vi.runAllTimersAsync();
    expect(seen).toContain("saving");
    expect(seen[seen.length - 1]).toBe("saved");
    expect(a.status().savedAt).toBeTypeOf("number");
  });

  test("a failed save surfaces error state with the message and retry re-runs it", async () => {
    const save = vi
      .fn()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(undefined);
    const a = createAutosave({ save, debounceMs: 800 });

    a.schedule();
    a.flush();
    await vi.runAllTimersAsync();
    expect(a.status().state).toBe("error");
    expect(a.status().message).toContain("boom");

    a.retry();
    await vi.runAllTimersAsync();
    expect(save).toHaveBeenCalledTimes(2);
    expect(a.status().state).toBe("saved");
  });

  test("retry with no prior failure is a no-op", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const a = createAutosave({ save, debounceMs: 800 });
    a.retry();
    await vi.runAllTimersAsync();
    expect(save).not.toHaveBeenCalled();
  });

  test("suspended autosave ignores schedule until resumed", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const a = createAutosave({ save, debounceMs: 800, suspended: true });

    a.schedule();
    vi.advanceTimersByTime(2000);
    await vi.runAllTimersAsync();
    expect(save).not.toHaveBeenCalled();

    a.setSuspended(false);
    a.schedule();
    vi.advanceTimersByTime(800);
    await vi.runAllTimersAsync();
    expect(save).toHaveBeenCalledTimes(1);
  });
});
