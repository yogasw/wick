import { describe, test, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import AgentAvatar from "../AgentAvatar.svelte";
import BlobAvatarPicker from "../BlobAvatarPicker.svelte";
import { clearStillCache } from "../blob.js";
import { stubCanvas, motion } from "./canvasStub.js";

const blob = { kind: "blob", shape: "cloud", expression: "happy", color: "#4b8fea" };
const node = (c: HTMLElement) => c.querySelector("[data-kind=blob]") as HTMLElement | null;

beforeEach(() => clearStillCache());
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("AgentAvatar kind=blob", () => {
  test("no kind keeps the classic svg", () => {
    const { container } = render(AgentAvatar, { props: { shape: "diamond", color: "#000000", still: true } });
    expect(container.querySelector("svg.agent-avatar")).not.toBeNull();
    expect(node(container)).toBeNull();
  });

  test("without a canvas it falls back to a colored dot", () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    const { container } = render(AgentAvatar, { props: { ...blob, size: 20 } });
    const el = node(container)!;
    expect(el.dataset.mode).toBe("dot");
    expect(container.querySelector("svg")).toBeNull();
  });

  test("a list avatar is a cached still image, even when working", () => {
    const { getContext } = stubCanvas();
    motion(false);
    const { container } = render(AgentAvatar, { props: { ...blob, size: 44, working: true } });
    const el = node(container)!;
    expect(el.tagName).toBe("IMG");
    expect(el.dataset.mode).toBe("still");
    expect(el.dataset.state).toBe("thinking");
    expect(container.querySelector("canvas")).toBeNull();
    // Two more rows with the same look reuse the drawn frame.
    render(AgentAvatar, { props: { ...blob, size: 44, working: true } });
    render(AgentAvatar, { props: { ...blob, size: 44, working: true } });
    expect(getContext).toHaveBeenCalledTimes(1);
  });

  test("live animates on a canvas", () => {
    stubCanvas();
    motion(false);
    const { container } = render(AgentAvatar, { props: { ...blob, size: 72, live: true } });
    const el = node(container)!;
    expect(el.querySelector("canvas")).not.toBeNull();
    expect(el.dataset.mode).toBe("live");
  });

  test("prefers-reduced-motion turns live into a still", () => {
    stubCanvas();
    motion(true);
    const { container } = render(AgentAvatar, { props: { ...blob, size: 72, live: true } });
    expect(node(container)!.dataset.mode).toBe("still");
  });

  test("a disabled agent sleeps", () => {
    stubCanvas();
    const { container } = render(AgentAvatar, { props: { ...blob, asleep: true } });
    expect(node(container)!.dataset.state).toBe("sleep");
  });
});

describe("BlobAvatarPicker", () => {
  test("picking a shape, expression and color reports the whole look", async () => {
    stubCanvas();
    const onChange = vi.fn();
    render(BlobAvatarPicker, { props: { shape: "cloud", expression: "happy", color: "#4b8fea", onChange } });
    expect(screen.getByRole("button", { name: "cloud" }).getAttribute("aria-pressed")).toBe("true");
    await fireEvent.click(screen.getByRole("button", { name: "flame" }));
    expect(onChange).toHaveBeenLastCalledWith({ shape: "flame", expression: "happy", color: "#4b8fea" });
    await fireEvent.click(screen.getByRole("button", { name: "sad" }));
    expect(onChange).toHaveBeenLastCalledWith({ shape: "cloud", expression: "sad", color: "#4b8fea" });
    await fireEvent.click(screen.getByRole("button", { name: "#e15b4a" }));
    expect(onChange).toHaveBeenLastCalledWith({ shape: "cloud", expression: "happy", color: "#e15b4a" });
  });

  test("shuffle picks a full look", async () => {
    stubCanvas();
    const onChange = vi.fn();
    render(BlobAvatarPicker, { props: { onChange } });
    await fireEvent.click(screen.getByTestId("blob-shuffle"));
    const l = onChange.mock.calls[0][0];
    expect(Object.keys(l).sort()).toEqual(["color", "expression", "shape"]);
  });

  test("24 previews, all still frames", () => {
    stubCanvas();
    const { container } = render(BlobAvatarPicker, { props: { onChange: vi.fn() } });
    expect(container.querySelectorAll("[data-mode=still]")).toHaveLength(24);
    expect(container.querySelector("canvas")).toBeNull();
  });
});
