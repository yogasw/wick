import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

import { BLOB_STATES } from "@wick-fe/common-avatar";
import AvatarEventsTable from "../AvatarEventsTable.svelte";

/* Classic and Image draw fewer poses than the blob: a blob-only default
   (read = wide, compact = swirl) shows as the pose they draw for it, and
   the stored override keeps the blob value. */
const pose = (ev: string) => screen.getByRole("combobox", { name: new RegExp(`^${ev} pose$`) }) as HTMLSelectElement;
afterEach(() => vi.unstubAllGlobals());

const options = () => [...document.querySelectorAll("[data-testid=avatar-events] select option")].map((o) => (o as HTMLOptionElement).value);

describe("Reacts to: classic style", () => {
  test("the read row shows orbit and moves as orbit", () => {
    render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", onChange: vi.fn() } });
    expect(pose("Reading / searching").value).toBe("orbit");
    expect(pose("Compacting context").value).toBe("thinking");
    expect(screen.getByTestId("avatar-event-read").querySelector("svg.agent-avatar")?.getAttribute("data-state")).toBe("orbit");
    expect(screen.getByTestId("avatar-event-compact").querySelector("svg.agent-avatar")?.getAttribute("data-state")).toBe("thinking");
  });

  test("the failed-tool row shows and moves as alert (fail)", () => {
    render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", onChange: vi.fn() } });
    expect(pose("A tool failed").value).toBe("alert");
    expect(pose("A tool failed").title).toBe("Blob pose: idle");
    expect(screen.getByTestId("avatar-event-error").querySelector("svg.agent-avatar")?.getAttribute("data-state")).toBe("alert");
  });

  test("idle picked on the failed-tool row is saved, not dropped as the blob default", async () => {
    const onChange = vi.fn();
    render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", onChange } });
    await fireEvent.change(pose("A tool failed"), { target: { value: "idle" } });
    expect(onChange).toHaveBeenLastCalledWith({ error: { state: "idle" } });
  });

  test("no blob-only option in any dropdown", () => {
    render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", events: { error: { state: "burst" } }, onChange: vi.fn() } });
    const all = options();
    for (const s of ["wide", "swirl", "burst", "egg"]) expect(all).not.toContain(s);
    expect(new Set(all)).toEqual(new Set(["idle", "thinking", "orbit", "alert", "notify", "sleep"]));
    expect(pose("A tool failed").value).toBe("notify");
    expect(pose("A tool failed").title).toBe("Blob pose: burst");
    expect(pose("Reading / searching").title).toBe("Blob pose: wide");
    expect(pose("Running a command / build").title).toBe(""); // thinking is drawn as is
  });

  test("editing another row keeps the stored blob pose", async () => {
    const onChange = vi.fn();
    render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", events: { read: { state: "play", expression: "suspicious" } }, onChange } });
    expect(pose("Reading / searching").value).toBe("orbit");
    await fireEvent.change(pose("Running a command / build"), { target: { value: "alert" } });
    expect(onChange).toHaveBeenLastCalledWith({ read: { state: "play", expression: "suspicious" }, run: { state: "alert" } });
  });
});

describe("Reacts to: image style", () => {
  test("blob-only defaults show and animate as their classic pose", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
    const image = { src: "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0 };
    render(AvatarEventsTable, { props: { kind: "image", shape: "circle", color: "#27b199", image, onChange: vi.fn() } });
    expect(pose("Reading / searching").value).toBe("orbit");
    expect(pose("Waiting on a remote agent").value).toBe("orbit");
    expect(options()).not.toContain("wide");
    expect(options()).not.toContain("swirl");
    expect(screen.getByTestId("avatar-event-read").querySelector("[data-testid=image-avatar]")?.getAttribute("data-pose")).toBe("orbit");
    expect(screen.getByTestId("avatar-event-compact").querySelector("[data-testid=image-avatar]")?.getAttribute("data-pose")).toBe("thinking");
    expect(pose("A tool failed").value).toBe("alert");
    expect(screen.getByTestId("avatar-event-error").querySelector("[data-testid=image-avatar]")?.getAttribute("data-pose")).toBe("alert");
  });
});

describe("Reacts to: blob style", () => {
  test("blob rows keep their own poses and the full blob list", () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    render(AvatarEventsTable, { props: { kind: "blob", shape: "cloud", color: "#8b5cf6", onChange: vi.fn() } });
    expect(pose("Reading / searching").value).toBe("wide");
    expect(pose("Compacting context").value).toBe("swirl");
    expect(pose("Reading / searching").title).toBe("");
    // The blob's failed tool stays idle with a sad face.
    expect(pose("A tool failed").value).toBe("idle");
    expect(screen.getByRole("combobox", { name: /^A tool failed expression$/ }) as HTMLSelectElement).toHaveProperty("value", "sad");
    expect([...pose("Reading / searching").options].map((o) => o.value)).toEqual([...BLOB_STATES]);
  });

  test("on the blob, idle on the failed-tool row is the default and saves as none", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    const onChange = vi.fn();
    render(AvatarEventsTable, { props: { kind: "blob", shape: "cloud", color: "#8b5cf6", events: { error: { state: "burst" } }, onChange } });
    await fireEvent.change(pose("A tool failed"), { target: { value: "idle" } });
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });
});
