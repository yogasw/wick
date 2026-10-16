import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

import { BLOB_STATES } from "@wick-fe/common-avatar";
import AvatarEventsTable from "../AvatarEventsTable.svelte";

/* Classic draws fewer poses than the blob: a blob-only default
   (read = wide, compact = swirl) shows as the pose it draws for it, and
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
  const image = { src: "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0 };
  const props = (extra: Record<string, unknown> = {}) => ({ kind: "image", shape: "circle", color: "#27b199", image, onChange: vi.fn(), ...extra });
  const drawn = (ev: string) => screen.getByTestId(`avatar-event-${ev}`).querySelector("[data-testid=image-avatar]")?.getAttribute("data-pose");
  const live = () => vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));

  test("the dropdown lists the 13 image poses, unmapped", () => {
    live();
    render(AvatarEventsTable, { props: props() });
    expect([...pose("Reading / searching").options].map((o) => o.value)).toEqual(["idle", "wide", "scan", "thinking", "type", "orbit", "send", "wait", "alert", "fail", "notify", "swirl", "squeeze"]);
    expect(options()).not.toContain("sleep");
    expect(screen.queryByRole("combobox", { name: /expression$/ })).toBeNull();
  });

  test("defaults as in the mockup, each row drawing its own pose", () => {
    live();
    render(AvatarEventsTable, { props: props() });
    const want: [string, string, string][] = [
      ["read", "Reading / searching", "scan"], ["run", "Running a command / build", "thinking"], ["write", "Writing / editing", "type"],
      ["delegate", "Delegating to a sub-agent", "send"], ["ask_user", "Waiting for you", "alert"], ["remote_wait", "Waiting on a remote agent", "wait"],
      ["error", "A tool failed", "fail"], ["done", "Turn finished", "notify"], ["compact", "Compacting context", "swirl"],
    ];
    for (const [ev, label, p] of want) {
      expect(pose(label).value).toBe(p);
      expect(pose(label).title).toBe("");
      expect(drawn(ev)).toBe(p);
    }
  });

  test("a pick is saved as the image pose; the image default saves as none", async () => {
    live();
    const onChange = vi.fn();
    render(AvatarEventsTable, { props: props({ onChange }) });
    await fireEvent.change(pose("Reading / searching"), { target: { value: "squeeze" } });
    expect(onChange).toHaveBeenLastCalledWith({ read: { state: "squeeze" } });
    // wide is the blob's default for read, not the image's: a real pick.
    await fireEvent.change(pose("Reading / searching"), { target: { value: "wide" } });
    expect(onChange).toHaveBeenLastCalledWith({ read: { state: "wide" } });
    await fireEvent.change(pose("Reading / searching"), { target: { value: "scan" } });
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });

  test("legacy picks (classic sleep, blob-only) show and draw as their look-alike", () => {
    live();
    render(AvatarEventsTable, { props: props({ events: { read: { state: "sleep" }, done: { state: "burst" }, run: { state: "orbit" } } }) });
    expect(pose("Reading / searching").value).toBe("wait");
    expect(pose("Reading / searching").title).toBe("Saved pose: sleep");
    expect(drawn("read")).toBe("wait");
    expect(pose("Turn finished").value).toBe("notify");
    expect(pose("Turn finished").title).toBe("Saved pose: burst");
    expect(pose("Running a command / build").value).toBe("orbit");
    expect(pose("Running a command / build").title).toBe("");
    expect(drawn("run")).toBe("orbit");
  });

  test("an off row falls back to the plain cue", () => {
    live();
    render(AvatarEventsTable, { props: props({ events: { read: { off: true } } }) });
    expect(pose("Reading / searching").disabled).toBe(true);
    expect(drawn("read")).toBe("orbit");
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

describe("Reacts to: across styles", () => {
  const image = { src: "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0 };
  const live = () => vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));

  test("an image pick equal to the blob default survives an edit from the blob table", async () => {
    live();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    const onImage = vi.fn();
    const first = render(AvatarEventsTable, { props: { kind: "image", shape: "circle", color: "#27b199", image, onChange: onImage } });
    await fireEvent.change(pose("Reading / searching"), { target: { value: "wide" } });
    const saved = onImage.mock.calls.at(-1)?.[0];
    expect(saved).toEqual({ read: { state: "wide" } });
    first.unmount();

    const onBlob = vi.fn();
    const blob = render(AvatarEventsTable, { props: { kind: "blob", shape: "cloud", color: "#8b5cf6", events: saved, onChange: onBlob } });
    await fireEvent.click(screen.getByRole("switch", { name: "Turn finished on" }));
    const afterBlob = onBlob.mock.calls.at(-1)?.[0];
    expect(afterBlob).toEqual({ read: { state: "wide" }, done: { off: true } });
    blob.unmount();

    // And from the classic table.
    const onClassic = vi.fn();
    render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", events: saved, onChange: onClassic } });
    await fireEvent.change(pose("Running a command / build"), { target: { value: "alert" } });
    expect(onClassic).toHaveBeenLastCalledWith({ read: { state: "wide" }, run: { state: "alert" } });
  });

  test("an image-only saved pose is named as saved on the classic and blob tables", () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    const events = { read: { state: "scan" } };
    const c = render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", events, onChange: vi.fn() } });
    expect(pose("Reading / searching").value).toBe("orbit");
    expect(pose("Reading / searching").title).toBe("Saved pose: scan");
    c.unmount();
    render(AvatarEventsTable, { props: { kind: "blob", shape: "cloud", color: "#8b5cf6", events, onChange: vi.fn() } });
    expect(pose("Reading / searching").value).toBe("wide");
    expect(pose("Reading / searching").title).toBe("Saved pose: scan");
    expect(pose("Running a command / build").title).toBe("");
  });
});
