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
});
