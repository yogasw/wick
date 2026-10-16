import { describe, test, expect } from "vitest";
import { render } from "@testing-library/svelte";
import ToastHost from "../ToastHost.svelte";
import { pushToast, toasts } from "@wick-fe/common-stores";

describe("ToastHost", () => {
  test("success toast uses the pos token family", () => {
    toasts.set([]);
    pushToast({ state: "ok", title: "Saved" });
    const { container } = render(ToastHost);
    const entry = container.querySelector('[aria-label="Dismiss notification"]') as HTMLElement;
    expect(entry.className).toContain("bg-pos-100");
    expect(entry.className).not.toContain("emerald");
  });

  test("info toast is neutral, themed for dark, and spins instead of a success glyph", () => {
    toasts.set([]);
    pushToast({ state: "info", title: "Finding message…", ttlMs: 0 });
    const { container } = render(ToastHost);
    const entry = container.querySelector('[aria-label="Dismiss notification"]') as HTMLElement;
    expect(entry.className).toContain("bg-white-100");
    expect(entry.className).toContain("dark:bg-navy-700");
    expect(entry.className).not.toContain("pos-");
    expect(entry.querySelector('[data-testid="toast-spinner"]')).not.toBeNull();
    expect(entry.textContent).not.toContain("✓");
  });
});
