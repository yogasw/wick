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
  onClose: noop,
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

  test("an edit is announced as unsaved", () => {
    render(PageEditor, { props: { ...(editorProps as object), canManage: true, draft: PAGE.body + "\nmore" } });
    expect(screen.getByText(/Unsaved changes/i)).toBeDefined();
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
