/* jsdom has no canvas 2D. stubCanvas installs a no-op context (every
   method a spy-free no-op, gradients included) and a fixed toDataURL, so
   the blob renderer can run its draw code in tests. */
import { vi } from "vitest";

export function stubCanvas() {
  const getContext = vi.fn(function (this: HTMLCanvasElement) {
    const canvas = this;
    return new Proxy({} as Record<string | symbol, unknown>, {
      get(target, prop) {
        if (prop === "canvas") return canvas;
        if (prop in target) return target[prop];
        if (prop === "createRadialGradient" || prop === "createLinearGradient") return () => ({ addColorStop() {} });
        return () => {};
      },
      set(target, prop, value) {
        target[prop] = value;
        return true;
      },
    });
  });
  const toDataURL = vi.fn(() => "data:image/png;base64,STILL");
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(getContext as never);
  vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockImplementation(toDataURL);
  return { getContext, toDataURL };
}

/** motion sets what prefers-reduced-motion answers. */
export function motion(reduced: boolean) {
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: reduced && q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {} }));
}
