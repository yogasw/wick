import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import AgentAvatar from "../AgentAvatar.svelte";
import { clearStillCache } from "../blob.js";
import { stubCanvas, motion } from "./canvasStub.js";

/* P55: an idle agent dozes in full colour with a "z"; only disabled greys. */
const src = "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef";
const image = { v: "0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0, src };
const svg = (c: HTMLElement) => c.querySelector("svg.agent-avatar") as SVGElement;
const doze = () => screen.queryByTestId("avatar-doze");
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("dozing avatar", () => {
  test("classic: eyes closed, full colour, floating z z", () => {
    const { container } = render(AgentAvatar, { props: { dozing: true, still: true } });
    expect(svg(container).getAttribute("data-state")).toBe("sleep");
    expect(svg(container).style.opacity).toBe("1");
    expect(doze()?.querySelector("[data-doze]")?.getAttribute("data-doze")).toBe("zz");
  });

  test("classic disabled: still greyed, no z", () => {
    const { container } = render(AgentAvatar, { props: { asleep: true, dozing: true, still: true } });
    expect(svg(container).getAttribute("data-state")).toBe("sleep");
    expect(svg(container).style.opacity).toBe("0.45");
    expect(doze()).toBeNull();
  });

  test("working wins: no doze, awake", () => {
    const { container } = render(AgentAvatar, { props: { dozing: true, working: true, still: true } });
    expect(svg(container).getAttribute("data-state")).toBe("thinking");
    expect(doze()).toBeNull();
  });

  test("image: picture untouched (idle pose) with the same floating z z, no badge", () => {
    render(AgentAvatar, { props: { kind: "image", image, dozing: true, still: true } });
    expect(screen.getByTestId("image-avatar").getAttribute("data-pose")).toBe("idle");
    const z = doze()!.querySelector("[data-doze]")!;
    expect(z.getAttribute("data-doze")).toBe("zz");
    expect(z.classList.contains("on-image")).toBe(true);
  });

  test("image disabled: greyed sleep pose, no badge", () => {
    render(AgentAvatar, { props: { kind: "image", image, asleep: true, still: true } });
    expect(screen.getByTestId("image-avatar").getAttribute("data-pose")).toBe("sleep");
    expect(doze()).toBeNull();
  });

  test("blob: sleeping still frame in full colour with z z; disabled stays faded", () => {
    clearStillCache();
    stubCanvas();
    motion(false);
    const blob = { kind: "blob", shape: "cloud", color: "#4b8fea", size: 44 };
    const { container } = render(AgentAvatar, { props: { ...blob, dozing: true } });
    const img = container.querySelector("[data-kind=blob]") as HTMLElement;
    expect(img.dataset.state).toBe("sleep");
    expect(img.style.opacity).toBe("1");
    expect(doze()?.querySelector("[data-doze]")?.getAttribute("data-doze")).toBe("zz");
    const off = render(AgentAvatar, { props: { ...blob, asleep: true } });
    expect((off.container.querySelector("[data-kind=blob]") as HTMLElement).style.opacity).toBe("0.55");
  });

  test("an event (Settings preview) never dozes", () => {
    render(AgentAvatar, { props: { dozing: true, event: "read", still: true } });
    expect(doze()).toBeNull();
  });

  test("needs attention wins over dozing: no z", () => {
    const { container } = render(AgentAvatar, { props: { dozing: true, alert: true, still: true } });
    expect(svg(container).getAttribute("data-state")).toBe("alert");
    expect(doze()).toBeNull();
  });

  test("a forced pose (Settings preview) never dozes", () => {
    render(AgentAvatar, { props: { dozing: true, pose: "idle", still: true } });
    expect(doze()).toBeNull();
  });
});
