import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import AvatarActivity from "../AvatarActivity.svelte";
import type { AvatarActivity as Activity } from "../../../avatarActivity.js";

/* P39: the CSS-only busy cues around another session's avatar. */
const avatar = createRawSnippet(() => ({ render: () => `<span data-testid="inner-avatar"></span>` }));

function motion(reduce: boolean) {
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: reduce && q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {} }));
}

function draw(activity: Activity, size: number, rings = false) {
  const r = render(AvatarActivity, { activity, size, rings, children: avatar });
  const root = screen.getByTestId("avatar-activity");
  const has = (id: string) => r.container.querySelector(`[data-testid=${id}]`) !== null;
  const body = root.querySelector(".av-body")!;
  return { r, root, has, body };
}

afterEach(() => vi.unstubAllGlobals());

describe("AvatarActivity", () => {
  test("keeps the avatar inside and says which state it shows", () => {
    motion(false);
    const { root, has } = draw("idle", 38);
    expect(root.dataset.activity).toBe("idle");
    expect(has("inner-avatar")).toBe(true);
    expect(has("avatar-orbit") || has("avatar-remote")).toBe(false);
    expect(root.classList.contains("alert")).toBe(false);
  });

  test("a tool wobbles with the orbit; thinking draws nothing here", () => {
    motion(false);
    let d = draw("tool", 38);
    expect(d.has("avatar-orbit")).toBe(true);
    expect(d.body.classList.contains("wobble")).toBe(true);
    d.r.unmount();
    d = draw("thinking", 38);
    expect(d.has("avatar-orbit") || d.has("avatar-remote")).toBe(false);
    expect(d.body.classList.contains("wobble")).toBe(false);
  });

  test("a small avatar only wobbles", () => {
    motion(false);
    const d = draw("tool", 16);
    expect(d.has("avatar-orbit")).toBe(false);
    expect(d.body.classList.contains("wobble")).toBe(true);
  });

  test("remote spins a dashed ring, alert pulses", () => {
    motion(false);
    let d = draw("remote", 16);
    expect(d.has("avatar-remote")).toBe(true);
    d.r.unmount();
    d = draw("alert", 16);
    expect(d.root.classList.contains("alert")).toBe(true);
    expect(d.body.classList.contains("wobble")).toBe(false);
  });

  test("rings: a self-animating avatar gets only the remote and alert rings", () => {
    motion(false);
    let d = draw("tool", 38, true);
    expect(d.has("avatar-orbit")).toBe(false);
    expect(d.body.classList.contains("wobble")).toBe(false);
    d.r.unmount();
    d = draw("alert", 38, true);
    expect(d.root.classList.contains("alert")).toBe(true);
    d.r.unmount();
    d = draw("remote", 38, true);
    expect(d.has("avatar-remote")).toBe(true);
  });

  test("reduced motion keeps the cue, standing still", () => {
    motion(true);
    const d = draw("tool", 38);
    expect(d.root.classList.contains("still")).toBe(true);
    expect(d.root.dataset.still).toBe("true");
    expect(d.has("avatar-orbit")).toBe(true);
  });

  test("full motion is not marked still", () => {
    motion(false);
    const d = draw("alert", 38);
    expect(d.root.classList.contains("still")).toBe(false);
    expect(d.root.dataset.still).toBeUndefined();
  });
});
