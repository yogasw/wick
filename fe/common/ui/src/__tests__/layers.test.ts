import { describe, test, expect, vi, afterEach } from "vitest";
import { pushLayer, layerDepth } from "../layers";

// The side panel's own handler, the way DetailView installs it: a window
// listener that closes on any Escape nobody prevented.
function panelStandIn() {
  const close = vi.fn();
  const onKey = (e: KeyboardEvent) => {
    if (e.key !== "Escape" || e.defaultPrevented) return;
    close();
  };
  window.addEventListener("keydown", onKey);
  return { close, dispose: () => window.removeEventListener("keydown", onKey) };
}

function press(el: EventTarget, key: string, init: KeyboardEventInit = {}) {
  const e = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  el.dispatchEvent(e);
  return e;
}

function box(): HTMLElement {
  const d = document.createElement("div");
  d.innerHTML = `<button id="a">a</button><input id="b" /><button id="c">c</button>`;
  document.body.appendChild(d);
  return d;
}

const cleanups: (() => void)[] = [];
afterEach(() => {
  while (cleanups.length) cleanups.pop()!();
  document.body.innerHTML = "";
});

describe("layers", () => {
  test("Escape closes only the top layer, and the panel underneath never hears it", () => {
    const panel = panelStandIn();
    cleanups.push(panel.dispose);
    const outer = vi.fn();
    const inner = vi.fn();
    cleanups.push(pushLayer(box(), { onEscape: outer }));
    const releaseInner = pushLayer(box(), { onEscape: inner });

    press(document.activeElement ?? document.body, "Escape");
    expect(inner).toHaveBeenCalledTimes(1);
    expect(outer).not.toHaveBeenCalled();
    expect(panel.close).not.toHaveBeenCalled();

    releaseInner();
    press(document.activeElement ?? document.body, "Escape");
    expect(outer).toHaveBeenCalledTimes(1);
    expect(panel.close).not.toHaveBeenCalled();
  });

  test("with no layer open, Escape reaches the panel", () => {
    const panel = panelStandIn();
    cleanups.push(panel.dispose);
    const release = pushLayer(box(), { onEscape: () => {} });
    release();
    expect(layerDepth()).toBe(0);
    press(document.body, "Escape");
    expect(panel.close).toHaveBeenCalledTimes(1);
  });

  test("an Escape a widget inside the layer already consumed leaves the layer open", () => {
    const node = box();
    const input = node.querySelector("#b") as HTMLInputElement;
    input.addEventListener("keydown", (e) => e.key === "Escape" && e.preventDefault());
    const onEscape = vi.fn();
    cleanups.push(pushLayer(node, { onEscape }));
    press(input, "Escape");
    expect(onEscape).not.toHaveBeenCalled();
  });

  test("onEscape returning false passes the key on", () => {
    const panel = panelStandIn();
    cleanups.push(panel.dispose);
    cleanups.push(pushLayer(box(), { onEscape: () => false }));
    press(document.body, "Escape");
    expect(panel.close).toHaveBeenCalledTimes(1);
  });

  test("focus moves into the layer on open and back to the opener on close", () => {
    const opener = document.createElement("button");
    document.body.appendChild(opener);
    opener.focus();
    const node = box();
    const release = pushLayer(node, { onEscape: () => {} });
    expect(node.contains(document.activeElement)).toBe(true);
    release();
    expect(document.activeElement).toBe(opener);
  });

  test("Tab wraps inside the layer", () => {
    const node = box();
    cleanups.push(pushLayer(node, { onEscape: () => {} }));
    const c = node.querySelector("#c") as HTMLElement;
    c.focus();
    const e = press(c, "Tab");
    expect(e.defaultPrevented).toBe(true);
    expect(document.activeElement?.id).toBe("a");
    const s = press(document.activeElement!, "Tab", { shiftKey: true });
    expect(s.defaultPrevented).toBe(true);
    expect(document.activeElement?.id).toBe("c");
  });
});
