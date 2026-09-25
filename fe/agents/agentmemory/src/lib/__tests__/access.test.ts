import { describe, test, expect, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";

import Overview from "../Overview.svelte";
import Projects from "../Projects.svelte";
import Settings from "../Settings.svelte";
import { emptySettings } from "../settings.js";
import type { ProjectRow, ProjectScope } from "../types.js";

/* PLAN §23 as the user meets it: admin MANAGES, everyone else LOOKS.
   
   What these assert is that a viewer's managing controls are ABSENT, not
   disabled — a dead button raises a question the screen cannot answer — and
   that the sentence explaining who may do it is there instead. The endpoints
   refuse a viewer either way; this is about not offering the click. */

const noop = () => {};

const overviewProps = {
  ov: null,
  loading: false,
  busy: false,
  test: null,
  installMsg: "",
  installFailed: false,
  onInstall: noop,
  onStart: noop,
  onStop: noop,
  onRestart: noop,
  onTest: noop,
};

const ROW: ProjectRow = { workspace: "wick", project: "kasir-8c28230d", page_count: 3 };

const projectsProps = {
  res: { projects: [ROW] },
  loading: false,
  busy: false,
  selected: "wick/kasir-8c28230d",
  handoffs: {},
  backfill: null,
  backfillError: "",
  scope: null,
  scopeError: "",
  onSelect: noop,
  onPreviewBackfill: noop,
  onRunBackfill: noop,
  onRefresh: noop,
  onGoSettings: noop,
};

const settingsProps = {
  form: emptySettings(),
  stored: emptySettings(),
  lock: null,
  defaultPort: 49374,
  running: true,
  restartPending: false,
  saving: false,
  busy: false,
  test: null,
  sweep: null,
  sweepError: "",
  advanced: false,
  onField: vi.fn(),
  onSave: noop,
  onReset: noop,
  onTest: noop,
  onCaptureAssistant: noop,
  onPreviewSweep: noop,
  onRunSweep: noop,
  onToggleAdvanced: noop,
};

const adminOnlyLine = () => screen.queryAllByText(/restricted to admins/i);

describe("Overview — daemon controls", () => {
  test("an admin gets them", () => {
    render(Overview, { props: { ...overviewProps, canManage: true } });
    expect(screen.getByText("Start")).toBeDefined();
    expect(screen.getByText("Test connection")).toBeDefined();
    expect(adminOnlyLine()).toHaveLength(0);
  });

  test("a viewer gets the sentence instead — no dead buttons", () => {
    render(Overview, { props: { ...overviewProps, canManage: false } });
    expect(screen.queryByText("Start")).toBeNull();
    expect(screen.queryByText("Stop")).toBeNull();
    expect(screen.queryByText("Restart")).toBeNull();
    expect(screen.queryByText("Test connection")).toBeNull();
    expect(adminOnlyLine().length).toBeGreaterThan(0);
  });

  test("the daemon's own readings stay readable for a viewer", () => {
    render(Overview, { props: { ...overviewProps, canManage: false } });
    expect(screen.getByText("Daemon")).toBeDefined();
    expect(screen.getByText("Version")).toBeDefined();
  });
});

describe("Projects — backfill", () => {
  test("an admin can preview and import", () => {
    render(Projects, { props: { ...projectsProps, canManage: true } });
    expect(screen.getByText("Preview")).toBeDefined();
    expect(screen.getByText("Import")).toBeDefined();
  });

  test("a viewer sees the project and its numbers, but no import", () => {
    render(Projects, { props: { ...projectsProps, canManage: false } });
    expect(screen.queryByText("Preview")).toBeNull();
    expect(screen.queryByText("Import")).toBeNull();
    expect(screen.getAllByText(/kasir-8c28230d/).length).toBeGreaterThan(0);
    expect(adminOnlyLine().length).toBeGreaterThan(0);
  });
});

describe("Settings — read-only for a viewer", () => {
  test("an admin gets the save bar", () => {
    render(Settings, { props: { ...settingsProps, canManage: true } });
    expect(screen.getByText("Save")).toBeDefined();
    expect(screen.getByText("Discard")).toBeDefined();
    expect(screen.getByText("Test connection")).toBeDefined();
  });

  test("a viewer gets neither the save bar nor the sweep, and no field accepts input", () => {
    const { container } = render(Settings, { props: { ...settingsProps, canManage: false } });
    expect(screen.queryByText("Save")).toBeNull();
    expect(screen.queryByText("Discard")).toBeNull();
    expect(screen.queryByText("Test connection")).toBeNull();
    expect(screen.queryByText("Run sweep")).toBeNull();
    expect(adminOnlyLine().length).toBeGreaterThan(0);

    // Every control is inert, so nothing can be typed into a form that has
    // no way to be submitted.
    const controls = container.querySelectorAll("input, select, textarea, button[role='switch']");
    expect(controls.length).toBeGreaterThan(0);
    for (const el of controls) {
      expect((el as HTMLInputElement).disabled).toBe(true);
    }
  });

  test("the values themselves are still on the page — a viewer may look", () => {
    render(Settings, { props: { ...settingsProps, canManage: false } });
    expect(screen.getByText("Store location")).toBeDefined();
    expect(screen.getByText("Port")).toBeDefined();
  });
});

describe("Projects — opened for one wick project (PLAN §22)", () => {
  const scopeOf = (source: ProjectScope["source"], project = "kasir-8c28230d"): ProjectScope => ({
    project_id: "8c28230d",
    name: "Kasir",
    folder: "/srv/projects/8c28230d/files",
    workspace: "wick",
    project,
    source,
  });

  test("a marker-pinned bucket is named, with no caveat", () => {
    render(Projects, { props: { ...projectsProps, canManage: true, scope: scopeOf("marker") } });
    const card = screen.getByTestId("scope-card");
    expect(card.textContent).toContain("Kasir");
    expect(card.textContent).toContain("wick/kasir-8c28230d");
    expect(card.textContent).toContain(".ai-memory.toml");
    expect(screen.queryByTestId("scope-caveat")).toBeNull();
  });

  test("wick's own mapping says the marker is still to be written", () => {
    render(Projects, { props: { ...projectsProps, canManage: true, scope: scopeOf("wick") } });
    expect(screen.getByTestId("scope-card").textContent).toContain("next time a session runs here");
    expect(screen.queryByTestId("scope-caveat")).toBeNull();
  });

  // The one source that can quietly merge two clients' memory.
  test("a basename-derived bucket carries the collision caveat", () => {
    render(Projects, { props: { ...projectsProps, canManage: true, scope: scopeOf("basename") } });
    expect(screen.getByTestId("scope-caveat").textContent).toMatch(/folder NAME/);
  });

  test("a project with no memory yet teaches the import rather than showing a zero", () => {
    render(Projects, {
      props: { ...projectsProps, canManage: true, selected: "", scope: scopeOf("wick", "brand-new-1f2e3d4c") },
    });
    const card = screen.getByTestId("scope-card");
    expect(card.textContent).toMatch(/Nothing has been captured into this bucket yet/);
    expect(screen.getByText("Preview import")).toBeDefined();
  });

  test("a viewer on an empty bucket is told why it is empty, without the import", () => {
    render(Projects, {
      props: { ...projectsProps, canManage: false, selected: "", scope: scopeOf("wick", "brand-new-1f2e3d4c") },
    });
    expect(screen.getByTestId("scope-card").textContent).toMatch(/Nothing has been captured/);
    expect(screen.queryByText("Preview import")).toBeNull();
    expect(adminOnlyLine().length).toBeGreaterThan(0);
  });

  test("a scope that could not be resolved says so instead of showing the whole store as if it were the project", () => {
    render(Projects, { props: { ...projectsProps, canManage: true, scopeError: "no wick project with id nope" } });
    expect(screen.queryByTestId("scope-card")).toBeNull();
    expect(screen.getByText(/could not be located/i)).toBeDefined();
  });
});

/* Installing the backend (PLAN §12.4).

   The control only exists for someone who can press it, and the sentence
   that replaces it names who can — the same rule as every other managing
   control on this page. */
describe("Overview — installing the backend", () => {
  const notInstalled = {
    backend: { id: "ai-memory", name: "ai-memory", blurb: "", has_data: true, github_url: "https://example.invalid/x" },
    daemon: {
      installed: false,
      version: "",
      running: false,
      managed: false,
      state: "not-installed",
      pref_port: 49374,
      bound_port: 49374,
      base_url: "http://127.0.0.1:49374",
    },
    resources: { rss_bytes: 0, rss_known: false, data_dir_bytes: 0, data_dir_known: false },
    autostart_lock: { locked: false },
  } as never;

  test("an admin gets the Install button, and is told what it will do", () => {
    render(Overview, { props: { ...overviewProps, ov: notInstalled, canManage: true } });
    // Found by its label, the way the other buttons on this card are: the
    // shared Button does not forward test ids.
    const btn = screen.getByRole("button", { name: /^Install/ });
    expect(btn.textContent).toContain("Install");
    expect(screen.getByTestId("install-block").textContent).toMatch(/verifies its published checksum/i);
    // No PATH homework: that instruction is what this feature replaced.
    expect(screen.getByTestId("install-block").textContent).not.toMatch(/put .* on PATH/i);
  });

  test("a viewer gets no button, and is told who installs it", () => {
    render(Overview, { props: { ...overviewProps, ov: notInstalled, canManage: false } });
    expect(screen.queryByRole("button", { name: /^Install/ })).toBeNull();
    expect(screen.getByTestId("install-block").textContent).toMatch(/restricted to admins/i);
  });

  test("the button says what it is doing while it runs", () => {
    render(Overview, { props: { ...overviewProps, ov: notInstalled, canManage: true, busy: true } });
    const btn = screen.getByRole("button", { name: /Installing/ }) as HTMLButtonElement;
    expect(btn.textContent).toContain("Installing…");
    expect(btn.disabled).toBe(true);
  });

  test("an installed backend offers no install at all", () => {
    const installed = {
      ...(notInstalled as object),
      daemon: { ...(notInstalled as { daemon: object }).daemon, installed: true, state: "stopped", version: "2.4.0" },
    } as never;
    render(Overview, { props: { ...overviewProps, ov: installed, canManage: true } });
    expect(screen.queryByTestId("install-block")).toBeNull();
  });

  test("the server's log is shown verbatim, and a failure reads as one", () => {
    const log = "downloading ai-memory-linux-x86_64.tar.gz (15.2 MiB) from v2.4.0";
    render(Overview, { props: { ...overviewProps, ov: notInstalled, canManage: true, installMsg: log } });
    expect(screen.getByTestId("install-result").textContent).toContain(log);
    expect(screen.getByTestId("install-result").className).not.toContain("rose");

    render(Overview, {
      props: { ...overviewProps, ov: notInstalled, canManage: true, installMsg: "checksum mismatch", installFailed: true },
    });
    const failed = screen.getAllByTestId("install-result").pop() as HTMLElement;
    expect(failed.className).toContain("rose");
    expect(failed.className).toContain("dark:");
  });
});
