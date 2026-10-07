import { describe, test, expect, vi, beforeEach } from "vitest";
import { copyText } from "../clipboard.js";

describe("copyText", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    // @ts-expect-error - resetting the shim between cases
    delete navigator.clipboard;
  });

  test("uses the async clipboard when the context allows it", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    expect(await copyText("hi")).toBe(true);
    expect(writeText).toHaveBeenCalledWith("hi");
  });

  /* wick is routinely served over plain http, where navigator.clipboard does
     not exist at all. Without this path every copy button in the app is a
     no-op on exactly the hosts people use it from. */
  test("falls back to execCommand when there is no clipboard API", async () => {
    const exec = vi.fn().mockReturnValue(true);
    // @ts-expect-error - jsdom has no execCommand
    document.execCommand = exec;
    expect(await copyText("hi")).toBe(true);
    expect(exec).toHaveBeenCalledWith("copy");
  });

  test("falls back when the clipboard API exists but rejects", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    Object.assign(navigator, { clipboard: { writeText } });
    const exec = vi.fn().mockReturnValue(true);
    // @ts-expect-error - jsdom has no execCommand
    document.execCommand = exec;
    expect(await copyText("hi")).toBe(true);
    expect(exec).toHaveBeenCalledWith("copy");
  });

  test("empty string copies nothing", async () => {
    expect(await copyText("")).toBe(false);
  });
});
