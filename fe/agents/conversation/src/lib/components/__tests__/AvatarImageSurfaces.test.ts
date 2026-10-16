import { describe, test, expect } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";
import RosterAvatar from "../team/RosterAvatar.svelte";
import DrawerHeader from "../DrawerHeader.svelte";
import { rosterStatus } from "../../rosterStatus.js";
import type { AgentItem } from "../../api/team.js";

/* M1: an image avatar shows its picture on every surface, through the
   avatar object the API sends (image.src), with no per-surface wiring. */
const SRC = "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef";
const avatar = { kind: "image", shape: "circle", color: "#27b199", image: { v: "0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0, src: SRC } };
const agent = {
  id: "a1", handle: "pic", name: "Pic", is_captain: false, project_id: "p1", features: {}, avatar, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null, last_preview: "", status: "idle",
} as unknown as AgentItem;

describe("image avatar on every surface", () => {
  test("roster row draws the versioned picture", () => {
    const r = render(RosterAvatar, { agent, st: rosterStatus(agent as never), open: false });
    const img = r.container.querySelector('[data-testid="image-avatar"] img');
    expect(img?.getAttribute("src")).toBe(SRC);
  });

  test("chat/drawer header draws the picture; a broken picture falls back to classic", async () => {
    const r = render(DrawerHeader, { title: "Pic", avatar, onClose: () => {} });
    const img = r.container.querySelector('[data-testid="image-avatar"] img') as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(SRC);
    await fireEvent.error(img);
    expect(r.container.querySelector('[data-testid="image-avatar"]')).toBeNull();
    expect(r.container.querySelector("svg.agent-avatar")).not.toBeNull();
  });

  test("a src that is not the avatar endpoint is never loaded", () => {
    const bad = { ...avatar, image: { ...avatar.image, src: "javascript:alert(1)" } };
    const r = render(DrawerHeader, { title: "Pic", avatar: bad, onClose: () => {} });
    expect(r.container.querySelector("img")).toBeNull();
    expect(r.container.querySelector("svg.agent-avatar")).not.toBeNull();
  });
});
