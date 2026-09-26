import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

import ProjectMemory from "../ProjectMemory.svelte";
import PageEditor from "../PageEditor.svelte";
import type { Checkpoint, ProjectScope } from "../types.js";

/* The project view as someone meets it (PLAN §22).

   Two things are worth pinning here and nothing else is: that the page is
   about ONE project and says which bucket that is, and that a viewer can
   read every word of it while changing none of it. */

const noop = () => {};

const SCOPE: ProjectScope = {
  project_id: "8c28230d",
  name: "Kasir",
  folder: "/srv/p/files",
  workspace: "wick",
  project: "kasir-8c28230d",
  source: "marker",
};

const BRIEFING = {
  counts: { pages_latest: 31, pages_all: 64, sessions: 12, observations: 880, evidence_rows: 210 },
  activity_7d: { days: 7, sessions: 3, observations: 120, pages_updated: 9 },
  activity_30d: { days: 30, sessions: 12, observations: 880, pages_updated: 31 },
  pending_handoff_count: 0,
  pending_message_count: 0,
  cross_project_dependents: 0,
  cross_project_dependencies: 0,
  recent_pages: [
    { path: "pages/kasir_prod_db.md", title: "kasir_prod_db", kind: "fact", updated_at: "2026-09-25T09:58:00Z" },
  ],
} as never;

const viewProps = {
  scope: SCOPE,
  scopeError: "",
  projects: { projects: [{ workspace: "wick", project: "kasir-8c28230d", page_count: 31 }] },
  briefing: BRIEFING,
  loading: false,
  busy: false,
  openPath: "",
  page: null,
  pageLoading: false,
  pageError: "",
  draft: "",
  draftTitle: "",
  draftKind: "",
  committedNote: "Every write is committed to the wiki's git history.",
  saveMsg: "",
  saveFailed: false,
  checkpoints: null,
  checkpointsLoading: false,
  search: null,
  searching: false,
  query: "",
  onQuery: noop,
  onSearch: noop,
  onOpen: noop,
  onClose: noop,
  onDraft: noop,
  onTitle: noop,
  onKind: noop,
  onSave: noop,
  onDelete: noop,
  onNewPage: noop,
  onLoadCheckpoints: noop,
  onRestore: noop,
  onRefresh: noop,
  onGoGlobal: noop,
} as never;

describe("ProjectView — about ONE project", () => {
  test("names the project and the bucket its agents write to", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    expect(screen.getByTestId("project-title").textContent).toContain("Kasir");
    expect(screen.getByText(/wick\/kasir-8c28230d/)).toBeDefined();
    // Where the bucket came from, in words — a marker, wick's mapping, or
    // the folder name.
    expect(screen.getByText(/ai-memory\.toml/)).toBeDefined();
  });

  test("shows this project's own counters", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    expect(screen.getByText("Observations")).toBeDefined();
    expect(screen.getByText("880")).toBeDefined();
  });

  test("lists the project's pages as cards", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    const cards = screen.getByTestId("page-cards");
    expect(cards.textContent).toContain("kasir_prod_db");
    expect(cards.textContent).toContain("pages/kasir_prod_db.md");
  });

  test("a basename-derived bucket carries the collision caveat", () => {
    render(ProjectMemory, {
      props: { ...(viewProps as object), canManage: true, scope: { ...SCOPE, source: "basename" } },
    });
    expect(screen.getByTestId("scope-caveat").textContent).toMatch(/folder NAME/);
  });

  test("an empty project teaches rather than showing a zero", () => {
    render(ProjectMemory, {
      props: { ...(viewProps as object), canManage: true, briefing: { ...(BRIEFING as object), recent_pages: [] } },
    });
    expect(screen.getByText(/Nothing has been written here yet/i)).toBeDefined();
  });

  test("a scope that could not be resolved says so instead of listing the store", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, scopeError: "no wick project with id x" } });
    expect(screen.getByText(/could not be located/i)).toBeDefined();
    expect(screen.queryByTestId("page-cards")).toBeNull();
  });

  test("an admin can start a new page; a viewer cannot", () => {
    const { unmount } = render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    expect(screen.getByText("Write a page")).toBeDefined();
    unmount();

    render(ProjectMemory, { props: { ...(viewProps as object), canManage: false } });
    expect(screen.queryByText("Write a page")).toBeNull();
    expect(screen.getAllByText(/restricted to admins/i).length).toBeGreaterThan(0);
  });

  test("a path the store would refuse is refused here, with the reason", async () => {
    const onNewPage = vi.fn();
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, onNewPage } });
    await fireEvent.input(screen.getByLabelText("New page path"), { target: { value: "notes/deploy" } });
    await fireEvent.click(screen.getByText("Write a page"));
    expect(onNewPage).not.toHaveBeenCalled();
    expect(screen.getByTestId("new-path-error").textContent).toMatch(/\.md/);
  });
});

/* Importing the history that predates capture, from the project's own tab.

   This is where an empty project is discovered, so this is where the fix has
   to be reachable — and the fix writes into the store, so the dangerous half
   is pinned: Preview never asks, Import always does, and a viewer sees
   neither button. */
describe("ProjectView — importing earlier sessions", () => {
  const withImport = (over: object = {}) => ({
    ...(viewProps as object),
    canManage: true,
    onPreviewBackfill: noop,
    onRunBackfill: noop,
    ...over,
  });

  // Import lives behind the switcher beside Activity, so every test here
  // opens that pane first — the same click the user makes.
  const openImport = async (over: object = {}) => {
    render(ProjectMemory, { props: withImport(over) as never });
    await fireEvent.click(screen.getByText("Import"));
  };

  test("the card explains what an import actually reads before offering one", async () => {
    await openImport();
    expect(screen.getByText(/one-time bootstrap, not a sync/i)).toBeDefined();
    expect(screen.getByTestId("backfill-selected-note").textContent).toMatch(/session files/i);
  });

  test("a preview runs straight away — it writes nothing", async () => {
    const onPreviewBackfill = vi.fn();
    await openImport({ onPreviewBackfill });
    await fireEvent.click(screen.getByText("Preview import"));
    expect(onPreviewBackfill).toHaveBeenCalledTimes(1);
  });

  test("an import is confirmed first, and the confirmation names the cost", async () => {
    const onRunBackfill = vi.fn();
    await openImport({ onRunBackfill });

    await fireEvent.click(screen.getByText("Import now"));
    expect(onRunBackfill).not.toHaveBeenCalled();
    // The question names the bucket being written to, and what the run costs.
    expect(screen.getByText(/into wick\/kasir-8c28230d/)).toBeDefined();
    expect(screen.getByText(/nothing is duplicated/)).toBeDefined();

    // Two "Import now" now exist — the card's button and the dialog's — and
    // the dialog's is the one that commits.
    const buttons = screen.getAllByText("Import now");
    await fireEvent.click(buttons[buttons.length - 1]);
    expect(onRunBackfill).toHaveBeenCalledTimes(1);
  });

  // Force is the one control that duplicates observations, so it is absent
  // unless the surface wired it, and it still asks first.
  test("a forced re-import is offered only when wired, and says what it costs", async () => {
    const onForceBackfill = vi.fn();
    await openImport({ onForceBackfill });
    await fireEvent.click(screen.getByText("Force re-import"));
    expect(onForceBackfill).not.toHaveBeenCalled();
    expect(screen.getByText(/observations do NOT/)).toBeDefined();

    const buttons = screen.getAllByText("Force re-import");
    await fireEvent.click(buttons[buttons.length - 1]);
    expect(onForceBackfill).toHaveBeenCalledTimes(1);
  });

  test("no force button on a surface that did not wire one", async () => {
    await openImport();
    expect(screen.queryByText("Force re-import")).toBeNull();
  });

  test("a viewer gets neither button", async () => {
    await openImport({ canManage: false });
    expect(screen.queryByText("Preview import")).toBeNull();
    expect(screen.queryByText("Import now")).toBeNull();
    expect(screen.getAllByText(/restricted to admins/i).length).toBeGreaterThan(0);
  });

  test("a surface that did not wire the import has no dead switcher", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    expect(screen.queryByTestId("pane-switch")).toBeNull();
    expect(screen.queryByText("Preview import")).toBeNull();
  });

  test("the outcome of a run is shown where it was started", async () => {
    await openImport({
      backfill: {
        selected: 4,
        imported_sessions: 4,
        imported_events: 31,
        skipped_for_cap: 0,
        failed_sessions: 0,
        skipped_non_empty: false,
        dry_run: false,
      },
    });
    expect(screen.getByTestId("backfill-summary").textContent).toMatch(/Imported/);
  });

  // The report that started all of this: 59 found, 0 imported, no explanation.
  test("a no-op preview says why nothing was imported", async () => {
    await openImport({
      backfill: {
        selected: 59,
        imported_sessions: 0,
        imported_events: 0,
        skipped_for_cap: 0,
        failed_sessions: 0,
        skipped_non_empty: true,
        dry_run: true,
      },
    });
    const text = screen.getByTestId("backfill-summary").textContent ?? "";
    expect(text).toContain("59 local sessions found for this project, none imported");
    expect(text).toMatch(/already has captured sessions/);
  });
});

const PAGE = { path: "pages/kasir_prod_db.md", body: "# kasir_prod_db\n\nRead replica.", title: "kasir_prod_db" };

const editorProps = {
  path: PAGE.path,
  page: PAGE,
  loading: false,
  error: "",
  busy: false,
  draft: PAGE.body,
  title: "kasir_prod_db",
  kind: "",
  committedNote: "Every write is committed to the wiki's git history, so this edit can be restored.",
  saveMsg: "",
  saveFailed: false,
  checkpoints: null,
  checkpointsLoading: false,
  onDraft: noop,
  onTitle: noop,
  onKind: noop,
  onSave: noop,
  onDelete: noop,
  onLoadCheckpoints: noop,
  onRestore: noop,
} as never;

describe("PageEditor — editing what an agent will recall", () => {
  test("an admin gets the body, the controls, and the promise that it is recoverable", () => {
    render(PageEditor, { props: { ...(editorProps as object), canManage: true } });
    expect(screen.getByLabelText("Page body")).toBeDefined();
    expect(screen.getByText("Save")).toBeDefined();
    expect(screen.getByText("Delete page")).toBeDefined();
    expect(screen.getByText("Restore…")).toBeDefined();
    // The reassurance sits WITH the edit box, which is where the nerve is.
    expect(screen.getByText(/can be restored/i)).toBeDefined();
  });

  test("Save is inert until something has actually changed", () => {
    render(PageEditor, { props: { ...(editorProps as object), canManage: true } });
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(true);
  });

  // The editor has no header of its own any more — the modal that opens it
  // names the page and announces the unsaved state (see the modal tests
  // below). What it still owns is whether Save is live.
  test("an edit makes Save live", () => {
    render(PageEditor, { props: { ...(editorProps as object), canManage: true, draft: PAGE.body + "\nmore" } });
    expect((screen.getByText("Save") as HTMLButtonElement).disabled).toBe(false);
  });

  test("a viewer reads the page and is offered nothing to change it with", () => {
    render(PageEditor, { props: { ...(editorProps as object), canManage: false } });
    expect(screen.getByText(/Read replica\./)).toBeDefined();
    expect(screen.queryByLabelText("Page body")).toBeNull();
    expect(screen.queryByText("Save")).toBeNull();
    expect(screen.queryByText("Delete page")).toBeNull();
    expect(screen.queryByText("Restore…")).toBeNull();
    expect(screen.getByText(/restricted to admins/i)).toBeDefined();
  });

  test("deleting asks first, and the question names the way back", async () => {
    const onDelete = vi.fn();
    render(PageEditor, { props: { ...(editorProps as object), canManage: true, onDelete } });
    await fireEvent.click(screen.getByText("Delete page"));
    expect(onDelete).not.toHaveBeenCalled();
    expect(screen.getByText(/stays in the wiki's git history/i)).toBeDefined();
  });

  test("the restore panel lists checkpoints, and prefers the ones naming this page", async () => {
    const checkpoints: Checkpoint[] = [
      { oid: "c84640f0a0f56e38", short_oid: "c84640f0a0f5", time: 1790344371, summary: "session 01a0c33c: hai" },
      {
        oid: "d544bc66fa5e33c4",
        short_oid: "d544bc66fa5e",
        time: 1790344784,
        summary: "write-page wick/demo: pages/kasir_prod_db.md",
      },
    ];
    render(PageEditor, { props: { ...(editorProps as object), canManage: true, checkpoints } });
    await fireEvent.click(screen.getByText("Restore…"));
    const panel = screen.getByTestId("restore-panel");
    const options = panel.querySelectorAll("option");
    // First option is the prompt; the page's own commit comes next.
    expect(options[1].textContent).toContain("d544bc66fa5e");
    expect((options[1] as HTMLOptionElement).value).toBe("d544bc66fa5e33c4");
  });

  test("a failed save reads as a failure, not as an outcome", () => {
    render(PageEditor, {
      props: { ...(editorProps as object), canManage: true, saveMsg: "daemon not running", saveFailed: true },
    });
    const msg = screen.getByTestId("save-result");
    expect(msg.textContent).toContain("daemon not running");
    expect(msg.className).toContain("rose");
    expect(msg.className).toContain("dark:");
  });
});

/* The per-project switch, and the analytics that are about THIS project. */

describe("ProjectMemory — the per-project switch", () => {
  const policy = {
    value: "" as const,
    allowed: true,
    trial_mode: false,
    reason: "Memory follows the provider instance.",
  };

  test("says whether this project records and recalls, with the server's reason", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, policy } });
    expect(screen.getByTestId("policy-state").textContent).toMatch(/Recording and recalling/i);
    expect(screen.getByTestId("policy-reason").textContent).toBe(policy.reason);
  });

  // The consequence has to be on screen while the decision is still being
  // made. Nobody flips this to start a host-wide trial; they flip it to try
  // one project — and the first person to do it would otherwise silently stop
  // capture in every other project with nothing having warned them.
  test("warns what switching on does to the rest of the host, BEFORE any trial exists", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, policy } });
    // Collapsed, because the sentence wraps in the markup and the assertion
    // is about the words, not where the template broke the line.
    const warn = (screen.getByTestId("policy-trial-warning").textContent ?? "").replace(/\s+/g, " ");
    expect(warn).toMatch(/starts a trial/i);
    expect(warn).toMatch(/stops recording/i);
    expect(warn).toMatch(/Nothing already stored is lost/i);
  });

  test("a viewer is not warned about a control they do not have", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: false, policy } });
    expect(screen.queryByTestId("policy-trial-warning")).toBeNull();
  });

  test("a project left out of a trial says so, and how to include it", () => {
    render(ProjectMemory, {
      props: {
        ...(viewProps as object),
        canManage: true,
        policy: {
          ...policy,
          allowed: false,
          trial_mode: true,
          trial_projects: ["kasir"],
          reason: "Agent Memory is being trialled on kasir, so projects that have not opted in are off.",
        },
      },
    });
    expect(screen.getByTestId("policy-state").textContent).toMatch(/Not recording/i);
    expect(screen.getByTestId("policy-reason").textContent).toContain("kasir");
    expect(screen.getByText(/one-project trial/i)).toBeDefined();
  });

  test("an admin can change it; a viewer sees the state and no control", () => {
    const onPolicy = vi.fn();
    const { unmount } = render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, policy, onPolicy } });
    expect(screen.getByRole("combobox")).toBeDefined();
    unmount();

    render(ProjectMemory, { props: { ...(viewProps as object), canManage: false, policy, onPolicy } });
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.getAllByText(/restricted to admins/i).length).toBeGreaterThan(0);
  });

  test("a host with no per-project switch shows no card rather than a broken one", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, policy: null } });
    expect(screen.queryByTestId("policy-card")).toBeNull();
  });
});

describe("ProjectMemory — analytics about this project", () => {
  test("draws the project's own write history and says what it counts", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    expect(screen.getAllByText(/page write/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/Page writes per day over the last 30 days/i)).toBeDefined();
    // The chart is labelled for anyone not reading it with their eyes.
    expect(screen.getByRole("img", { name: /Page writes per day/i })).toBeDefined();
  });

  test("names the backend the memory lives in", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, backendName: "ai-memory" } });
    expect(screen.getAllByText(/ai-memory/).length).toBeGreaterThan(0);
  });

  // The rule that keeps a store-wide number from being relabelled as this
  // project's: no briefing means the absence is stated.
  test("a project with no briefing gets the stated absence, not a chart of zeros", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, briefing: null } });
    expect(screen.getByTestId("no-briefing").textContent).toMatch(/briefing/i);
  });

  test("a stale project is called out with what to check", () => {
    const stale = {
      ...(BRIEFING as object),
      last_observation_at: new Date(Date.now() - 40 * 86_400_000).toISOString(),
    };
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true, briefing: stale } });
    expect(screen.getByTestId("freshness")).toBeDefined();
    expect(screen.getByText(/capture hook/i)).toBeDefined();
  });
});

/* The regrouped page (Yoga, 2026-09-26).

   Three complaints, three things to pin: the list is paged instead of poured
   out whole, opening a page puts the reader BESIDE the list rather than under
   it, and the policy card no longer claims a project is recording on a host
   where nothing is wired to record. */

const manyPages = Array.from({ length: 23 }, (_, i) => ({
  path: `sessions/${String(i).padStart(4, "0")}.md`,
  title: `session ${i}`,
  kind: "session",
  updated_at: "2026-09-25T09:58:00Z",
}));

const withPages = (over: object = {}) => ({
  ...(viewProps as object),
  canManage: true,
  briefing: { ...(BRIEFING as object), recent_pages: manyPages },
  ...over,
});

describe("ProjectMemory — the page list is paged, not poured out", () => {
  test("only one page of cards is rendered, and the range says so", () => {
    render(ProjectMemory, { props: withPages() as never });
    expect(screen.getByTestId("page-cards").querySelectorAll("li")).toHaveLength(10);
    expect(screen.getByTestId("pager").textContent).toContain("1–10 of 23 pages");
  });

  test("Next moves the window without touching the rest of the page", async () => {
    render(ProjectMemory, { props: withPages() as never });
    await fireEvent.click(screen.getByText("Next"));
    expect(screen.getByTestId("pager").textContent).toContain("11–20 of 23 pages");
    expect(screen.getByTestId("page-cards").textContent).toContain("session 10");
    expect(screen.getByTestId("page-cards").textContent).not.toContain("session 0\n");
  });

  test("a list that fits on one page gets the count and no controls", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    expect(screen.getByTestId("pager").textContent).toContain("1–1 of 1 page");
    expect(screen.queryByText("Next")).toBeNull();
  });
});

/* Reading a page is a modal over the tab, not a column beside it.

   The column was tried and was worse: a ~300px strip for a page of markdown
   inside an already-narrow settings shell, plus a box occupying the same
   space to say that nothing was open (Yoga, 2026-09-26: "kecil bet gini"). */

const OPEN = {
  openPath: "sessions/0000.md",
  page: { path: "sessions/0000.md", body: "hello" },
  // draft matches the stored body: this is a page just opened, not an edit.
  draft: "hello",
};

describe("ProjectMemory — reading a page opens a modal", () => {
  test("nothing open: nothing occupies space to say so", () => {
    render(ProjectMemory, { props: withPages() as never });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByTestId("page-editor")).toBeNull();
    // The list has the tab to itself.
    expect(screen.getByTestId("page-cards")).toBeDefined();
  });

  test("opening a page puts it in ONE dialog, over the list", () => {
    render(ProjectMemory, { props: withPages(OPEN) as never });
    const dialog = screen.getByRole("dialog");
    expect(dialog).toBeDefined();
    // One editor, not one per breakpoint — there is no second copy to drift.
    expect(screen.getAllByTestId("page-editor")).toHaveLength(1);
    expect(screen.getAllByLabelText("Page body")).toHaveLength(1);
    expect(dialog.contains(screen.getByTestId("page-editor"))).toBe(true);
    // The list is still there behind it, untouched.
    expect(screen.getByTestId("page-cards")).toBeDefined();
  });

  test("the dialog names the page, and every editor capability is in it", () => {
    render(ProjectMemory, { props: withPages({ ...OPEN, canManage: true }) as never });
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("sessions/0000.md");
    for (const label of ["Save", "Delete page", "Restore…"]) {
      expect(dialog.textContent).toContain(label);
    }
    expect(screen.getByLabelText("Page title")).toBeDefined();
    expect(screen.getByLabelText("Page body")).toBeDefined();
    // The promise that an edit is recoverable travels with the editor.
    expect(dialog.textContent).toMatch(/committed to the wiki's git history/i);
  });

  test("a clean draft closes straight away, from the × and from Escape", async () => {
    const onClose = vi.fn();
    const { unmount } = render(ProjectMemory, { props: withPages({ ...OPEN, onClose }) as never });
    await fireEvent.click(screen.getByLabelText("Close"));
    expect(onClose).toHaveBeenCalledTimes(1);
    unmount();

    onClose.mockClear();
    render(ProjectMemory, { props: withPages({ ...OPEN, onClose }) as never });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // Escape and a backdrop click are both one stray gesture away, so what was
  // typed is worth a question rather than a silent loss.
  test("an unsaved draft is not discarded without asking", async () => {
    const onClose = vi.fn();
    render(ProjectMemory, {
      props: withPages({ ...OPEN, draft: "hello — and something new", onClose }) as never,
    });
    expect(screen.getByTestId("unsaved-marker")).toBeDefined();

    await fireEvent.click(screen.getByLabelText("Close"));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByText(/Discard your unsaved edits\?/i)).toBeDefined();

    // Keeping the edit leaves the reader open with the draft still in it.
    await fireEvent.click(screen.getByText("Keep editing"));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Page body")).toBeDefined();

    // Saying it out loud is what discards it.
    await fireEvent.click(screen.getByLabelText("Close"));
    await fireEvent.click(screen.getByText("Discard them"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test("Escape on a dirty draft asks too, rather than throwing the edit away", async () => {
    const onClose = vi.fn();
    render(ProjectMemory, { props: withPages({ ...OPEN, draft: "edited", onClose }) as never });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByText(/Discard your unsaved edits\?/i)).toBeDefined();
  });

  // The difference between a modal and a div that looks like one.
  test("opening takes focus, and closing hands it back to the row", async () => {
    const props = withPages({ canManage: true });
    const { rerender } = render(ProjectMemory, { props: props as never });

    const row = screen.getByTestId("page-cards").querySelector("button") as HTMLButtonElement;
    row.focus();
    expect(document.activeElement).toBe(row);

    await rerender({ ...props, ...OPEN } as never);
    const dialog = screen.getByRole("dialog");
    expect(dialog.contains(document.activeElement)).toBe(true);

    await rerender({ ...props, openPath: "", page: null } as never);
    expect(document.activeElement).toBe(row);
  });
});

describe("ProjectMemory — the switch this project cannot reach", () => {
  const policy = {
    value: "on" as const,
    allowed: true,
    trial_mode: false,
    reason: "Agent Memory is on for this project.",
  };

  test("a host with nothing wired does not show a green 'recording'", () => {
    render(ProjectMemory, {
      props: {
        ...(viewProps as object),
        canManage: true,
        policy: { ...policy, providers: { known: true, instances: 0, recording: 0 } },
      },
    });
    expect(screen.getByTestId("policy-state").textContent).toMatch(/Nothing is recording/i);
    expect(screen.getByTestId("provider-gap").textContent).toMatch(/Providers/);
  });

  test("a wired host says nothing extra", () => {
    render(ProjectMemory, {
      props: {
        ...(viewProps as object),
        canManage: true,
        policy: { ...policy, providers: { known: true, instances: 1, recording: 1 } },
      },
    });
    expect(screen.getByTestId("policy-state").textContent).toMatch(/Recording and recalling/i);
    expect(screen.queryByTestId("provider-gap")).toBeNull();
  });
});

describe("ProjectMemory — analytics shared with the global panel", () => {
  test("the project's own totals lead the page, with the ratios spelled out", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    const grid = screen.getByTestId("project-counters");
    expect(grid.textContent).toContain("Observations");
    expect(grid.textContent).toContain("880");
    expect(grid.textContent).toContain("73.3 per session");
  });

  test("both activity windows are shown, labelled", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    const windows = screen.getByTestId("project-windows");
    expect(windows.textContent).toContain("Last 7 days");
    expect(windows.textContent).toContain("3 · 120 · 9");
  });

  // What is NOT per project is said in words rather than drawn as this
  // project's zeros.
  test("store-only analytics are named as absent, not rendered as zeros", () => {
    render(ProjectMemory, { props: { ...(viewProps as object), canManage: true } });
    expect(screen.getByTestId("store-only-note").textContent).toMatch(/no per-project figure/i);
  });
});
