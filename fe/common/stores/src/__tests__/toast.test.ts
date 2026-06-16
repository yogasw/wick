import { describe, test, expect, vi, beforeEach } from "vitest";
import { get } from "svelte/store";

/* Reset module state between tests so the nextID counter and toasts array
   don't bleed across test cases. */
beforeEach(async () => {
  vi.resetModules();
});

describe("toast store", () => {
  test("starts empty", async () => {
    const { toasts } = await import("../toast.js");
    expect(get(toasts)).toHaveLength(0);
  });

  test("pushToast adds a toast", async () => {
    const { toasts, pushToast } = await import("../toast.js");
    pushToast({ state: "ok", title: "Saved" });
    expect(get(toasts)).toHaveLength(1);
    expect(get(toasts)[0].title).toBe("Saved");
  });

  test("dismissToast removes by id", async () => {
    const { toasts, pushToast, dismissToast } = await import("../toast.js");
    const id = pushToast({ state: "ok", title: "Hello" });
    dismissToast(id);
    expect(get(toasts)).toHaveLength(0);
  });

  test("toastOk shorthand sets state to ok", async () => {
    const { toasts, toastOk } = await import("../toast.js");
    toastOk("Done");
    expect(get(toasts)[0].state).toBe("ok");
  });

  test("toastError shorthand sets state to error", async () => {
    const { toasts, toastError } = await import("../toast.js");
    toastError("Failed");
    expect(get(toasts)[0].state).toBe("error");
  });

  test("duplicate toasts deduplicated", async () => {
    const { toasts, pushToast } = await import("../toast.js");
    pushToast({ state: "ok", title: "Same" });
    pushToast({ state: "ok", title: "Same" });
    expect(get(toasts)).toHaveLength(1);
  });
});
