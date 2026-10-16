import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import ImageAvatar from "../ImageAvatar.svelte";
import AgentAvatar from "../AgentAvatar.svelte";
import { IMAGE_POSES, imageAvatarPose, imagePoseFor } from "../image.js";

const src = "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef";
const image = (crop?: string) => ({ v: "0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0, src, ...(crop ? { crop } : {}) });
const el = () => screen.getByTestId("image-avatar");
const marks = () => [...el().querySelectorAll("[data-fx]")].map((m) => m.getAttribute("data-fx"));
afterEach(() => vi.unstubAllGlobals());

/* The marks each pose draws around the frame, as the mockup's AvatarFx. */
const MARKS: Record<string, string[]> = {
  idle: [], wide: [], scan: ["scan"], thinking: ["dots"], type: ["cur"], orbit: ["orbit"], send: ["send"], wait: ["wave"],
  alert: ["ring", "badge"], fail: ["ring", "badge"], notify: ["ping"], swirl: [], squeeze: [],
};

describe("image poses", () => {
  test("13 poses, in the mockup's order", () => {
    expect([...IMAGE_POSES]).toEqual(["idle", "wide", "scan", "thinking", "type", "orbit", "send", "wait", "alert", "fail", "notify", "swirl", "squeeze"]);
  });

  test("legacy and blob-only names map to their look-alike; unknown is idle", () => {
    for (const p of IMAGE_POSES) expect(imagePoseFor(p)).toBe(p);
    expect(imagePoseFor("sleep")).toBe("wait");
    expect(imagePoseFor("egg")).toBe("idle");
    expect(imagePoseFor("play")).toBe("orbit");
    expect(imagePoseFor("comet")).toBe("send");
    expect(imagePoseFor("burst")).toBe("notify");
    expect(imagePoseFor("wink")).toBe("notify");
    expect(imagePoseFor("exclaim")).toBe("alert");
    expect(imagePoseFor("bogus")).toBe("idle");
    expect(imagePoseFor(undefined)).toBe("idle");
    // The disabled / hatching states stay states in the avatar itself.
    expect(imageAvatarPose("sleep")).toBe("sleep");
    expect(imageAvatarPose("egg")).toBe("egg");
    expect(imageAvatarPose("burst")).toBe("notify");
  });

  test.each(IMAGE_POSES.map((p) => [p]))("%s: its own pose and marks", (p) => {
    render(ImageAvatar, { props: { image: image(), pose: p, animate: true } });
    expect(el().getAttribute("data-pose")).toBe(p);
    expect(el().classList.contains("animate")).toBe(true);
    expect(marks()).toEqual(MARKS[p]);
  });

  test("the badge says ? on alert and ! on fail", () => {
    const { unmount } = render(ImageAvatar, { props: { image: image(), pose: "alert", animate: true } });
    expect(el().querySelector("[data-fx=badge]")?.textContent).toBe("?");
    unmount();
    render(ImageAvatar, { props: { image: image(), pose: "fail", animate: true } });
    expect(el().querySelector("[data-fx=badge]")?.textContent).toBe("!");
  });

  test.each(IMAGE_POSES.map((p) => [p]))("%s still (reduced motion): the same marks, no motion", (p) => {
    render(ImageAvatar, { props: { image: image(), pose: p } });
    expect(el().classList.contains("animate")).toBe(false);
    expect(marks()).toEqual(MARKS[p]);
  });

  test("marks sit outside the clip: every crop keeps its clip and ring", () => {
    for (const crop of ["round", "rounded", "hex", "none"]) {
      const { unmount } = render(ImageAvatar, { props: { image: image(crop), pose: "fail", animate: true } });
      const clip = el().querySelector<HTMLElement>(".frame > .clip")!;
      expect(clip.querySelector("[data-fx]")).toBeNull();
      expect(el().querySelector(".frame [data-fx]")).toBeNull();
      expect(el().querySelector(".frame")?.getAttribute("data-crop")).toBe(crop);
      unmount();
    }
  });

  test("a legacy name draws its look-alike", () => {
    render(ImageAvatar, { props: { image: image(), pose: "burst", animate: true } });
    expect(el().getAttribute("data-pose")).toBe("notify");
  });

  test("AgentAvatar: an image pose is drawn as is, moving", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
    render(AgentAvatar, { props: { kind: "image", shape: "circle", color: "#27b199", image: image(), pose: "squeeze", live: true } });
    expect(el().getAttribute("data-pose")).toBe("squeeze");
    expect(el().classList.contains("animate")).toBe(true);
  });

  test("AgentAvatar: prefers-reduced-motion draws one still frame", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {} }));
    render(AgentAvatar, { props: { kind: "image", shape: "circle", color: "#27b199", image: image(), pose: "scan", live: true } });
    expect(el().getAttribute("data-pose")).toBe("scan");
    expect(el().classList.contains("animate")).toBe(false);
  });

  test("AgentAvatar: a running Read on an image avatar scans; a disabled one sleeps", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
    const { unmount } = render(AgentAvatar, { props: { kind: "image", shape: "circle", color: "#27b199", image: image(), working: true, toolName: "Read", live: true } });
    expect(el().getAttribute("data-pose")).toBe("scan");
    unmount();
    render(AgentAvatar, { props: { kind: "image", shape: "circle", color: "#27b199", image: image(), asleep: true, live: true } });
    expect(el().getAttribute("data-pose")).toBe("sleep");
  });
});
