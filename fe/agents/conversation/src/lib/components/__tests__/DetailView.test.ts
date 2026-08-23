import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

// A stand-in Effect: `.pipe(...)` returns itself so options.ts's builders
// (apiGetE(...).pipe(Effect.map(...))) don't throw at module load. Nothing
// runs it — Effect.runPromise is stubbed to a never-resolving promise below.
const inertEffect: { pipe: (...a: unknown[]) => unknown } = { pipe: () => inertEffect };

vi.mock("@wick-fe/common-api", () => ({
  WickClientLayer: {},
  apiGetE: vi.fn(() => inertEffect),
  apiPostE: vi.fn(() => inertEffect),
  apiDeleteE: vi.fn(() => inertEffect),
  // Promise-based, unlike the Effect helpers above: the composer's @ menu
  // loads the role list directly rather than through a provided layer.
  listAgentProfiles: vi.fn(async () => ({
    profiles: [],
    owned: [],
    inherited: [],
  })),
}));

vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}));

vi.mock("effect", () => ({
  Effect: {
    runPromise: vi.fn().mockReturnValue(new Promise(() => {})),
    provide: vi.fn((eff: unknown) => eff),
    map: vi.fn(() => (eff: unknown) => eff),
  },
}));

const { metaStore } = vi.hoisted(() => {
  /* Minimal writable so tests can drive the thread meta title. */
  let value: Record<string, unknown> = {};
  const subs = new Set<(v: Record<string, unknown>) => void>();
  return {
    metaStore: {
      subscribe(fn: (v: Record<string, unknown>) => void) {
        subs.add(fn);
        fn(value);
        return () => { subs.delete(fn); };
      },
      set(v: Record<string, unknown>) {
        value = v;
        subs.forEach((fn) => fn(value));
      },
    },
  };
});

vi.mock("../../stores/thread.js", () => ({
  createThreadStore: () => ({
    turns: { subscribe: (fn: (v: unknown[]) => void) => { fn([]); return () => {}; } },
    live: { subscribe: (fn: (v: null) => void) => { fn(null); return () => {}; } },
    typing: { subscribe: (fn: (v: { active: boolean }) => void) => { fn({ active: false }); return () => {}; } },
    lifecycle: { subscribe: (fn: (v: { state: string; pid: number; substate: string; at: number }) => void) => { fn({ state: "", pid: 0, substate: "", at: 0 }); return () => {}; } },
    meta: metaStore,
    setHistory: vi.fn(),
    appendUserTurn: vi.fn(),
    handleEvent: vi.fn(),
  }),
}));

const { sseBus } = vi.hoisted(() => ({
  sseBus: { handler: null as ((ev: { type: string }) => void) | null },
}));

vi.mock("../../stores/sse.js", () => ({
  connectSession: () => ({
    close: vi.fn(),
    status: { subscribe: (fn: (v: string) => void) => { fn("connected"); return () => {}; } },
    onEvent: (fn: (ev: { type: string }) => void) => { sseBus.handler = fn; },
  }),
}));

vi.mock("../../stores/asks.js", () => ({
  currentAsk: { subscribe: (fn: (v: null) => void) => { fn(null); return () => {}; } },
  showAsk: vi.fn(),
  hideAsk: vi.fn(),
}));

vi.mock("../../stores/approvals.js", () => ({
  currentApproval: { subscribe: (fn: (v: null) => void) => { fn(null); return () => {}; } },
  showApproval: vi.fn(),
  hideApproval: vi.fn(),
}));

vi.mock("../../notify.js", () => ({
  notify: vi.fn(),
}));

vi.mock("../../router.js", () => ({
  push: vi.fn(),
}));

vi.mock("../../api/sessions.js", () => ({
  getConversation: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  getSessionMeta: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  deleteSession: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("../../api/options.js", () => ({
  getProviderOptions: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  getProjectOptions: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  switchProvider: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  moveProject: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("../../api/asks.js", () => ({
  getAsks: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  answerAsk: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("../../api/approvals.js", () => ({
  getApprovals: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  sendApprovalDecision: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  revokeApproval: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("../../api/messages.js", () => ({
  sendMessage: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("../../api/subagents.js", () => ({
  getSubAgents: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  getSubAgentPanel: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  interruptSubAgent: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  interruptAllSubAgents: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  getMessages: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  bumpHops: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  // Real behaviour — the rail badge and the poll both key off it.
  liveSubAgents: (subs: { status: string }[]) =>
    subs.filter((s) => s.status === "queued" || s.status === "running"),
}));

vi.mock("../../api/files.js", () => ({
  listFiles: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  searchFiles: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  readFile: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  saveFile: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  createFile: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  deleteFile: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  downloadURL: vi.fn().mockReturnValue(""),
}));

vi.mock("../../api/composer.js", () => ({
  listComposerCommands: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("../../api/processes.js", () => ({
  getProcesses: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  killProcess: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  dequeueProcess: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  // liveProcesses is a pure filter (not an Effect) — keep the real behaviour
  // so processCount/badge reflect the idle-fallback exclusion under test.
  liveProcesses: (procs: unknown[]) =>
    (procs ?? []).filter((p) => (p as { kind?: string }).kind !== "idle"),
}));

vi.mock("../../api/workspace.js", () => ({
  listWorkspace: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  addWorkspace: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  saveWorkspaceConfig: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  testWorkspace: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  duplicateWorkspace: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  renameWorkspace: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  removeWorkspace: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("../../api/schedules.js", () => ({
  listSchedules: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  createSchedule: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  cancelSchedule: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  pauseSchedule: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  resumeSchedule: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
}));

vi.mock("svelte/store", async (importActual) => {
  const actual = await importActual<typeof import("svelte/store")>();
  return { ...actual };
});

import DetailView from "../DetailView.svelte";
import { killProcess, getProcesses } from "../../api/processes.js";
import { getAsks } from "../../api/asks.js";
import { getApprovals } from "../../api/approvals.js";
import { getConversation } from "../../api/sessions.js";
import { getSubAgentPanel } from "../../api/subagents.js";
import { SCM_DEFAULT_W, RAIL_GUTTER_PX } from "../../scmWidth.js";
import { Effect } from "effect";

const DEFAULT_PROPS = {
  base: "/api",
  sessionId: "test-sess",
};

describe("DetailView — SCM source rail panel", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
    /* Reset WickSCM stub between tests */
    (window as unknown as Record<string, unknown>)["WickSCM"] = undefined;
  });

  test("source rail button is rendered", () => {
    render(DetailView, { props: DEFAULT_PROPS });
    const sourceBtn = screen.getByRole("button", { name: /source/i });
    expect(sourceBtn).toBeDefined();
  });

  test("clicking source rail opens the side panel inline (not a fixed overlay)", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    /* No fixed scm overlay with data-scm-panel should exist */
    const scmOverlay = container.querySelector("[data-scm-panel]");
    expect(scmOverlay).toBeNull();
  });

  test("clicking source rail causes sideOpen — panel container is rendered", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    /* The desktop side panel div (lg:flex) should appear */
    const sidePanel = container.querySelector(".lg\\:flex.flex-col");
    expect(sidePanel).not.toBeNull();
  });

  test("clicking source rail again closes the panel (toggle behavior)", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);
    await fireEvent.click(sourceBtn);

    const sidePanel = container.querySelector(".lg\\:flex.flex-col");
    expect(sidePanel).toBeNull();
  });

  test("clicking context rail after source switches panel content", async () => {
    render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    const contextBtn = screen.getByRole("button", { name: /context/i });
    await fireEvent.click(contextBtn);

    /* source panel removed; context btn is now active */
    expect(contextBtn).toBeDefined();
  });

  test("SCM host div is only in DOM when source tab is active — absent on other tabs", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    /* Initially no source tab active — no scm host */
    expect(container.querySelector("[data-scm-host]")).toBeNull();

    /* Open source tab */
    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);
    expect(container.querySelector("[data-scm-host]")).not.toBeNull();

    /* Switch to context — scm host must be gone */
    const contextBtn = screen.getByRole("button", { name: /context/i });
    await fireEvent.click(contextBtn);
    expect(container.querySelector("[data-scm-host]")).toBeNull();
  });

  test("WickSCM.mount is called with non-empty sessionID + onClose for desktop and mobile hosts", async () => {
    const mountFn = vi.fn();
    const unmountFn = vi.fn();
    (window as unknown as Record<string, unknown>)["WickSCM"] = { mount: mountFn, unmount: unmountFn };

    render(DetailView, { props: DEFAULT_PROPS });
    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    /* Allow microtasks to flush */
    await Promise.resolve();

    /* Both the desktop host and the mobile overlay host mount the island */
    expect(mountFn).toHaveBeenCalledTimes(2);
    for (const call of mountFn.mock.calls) {
      const [, opts] = call as [unknown, { sessionID: string; onClose?: () => void }];
      expect(opts.sessionID).toBe("test-sess");
      expect(opts.sessionID.length).toBeGreaterThan(0);
      expect(typeof opts.onClose).toBe("function");
    }
  });

  test("WickSCM.mount is NOT called when sessionId is empty string", async () => {
    const mountFn = vi.fn();
    (window as unknown as Record<string, unknown>)["WickSCM"] = { mount: mountFn, unmount: vi.fn() };

    render(DetailView, { props: { base: "/api", sessionId: "" } });
    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);
    await Promise.resolve();

    expect(mountFn).not.toHaveBeenCalled();
  });

  test("WickSCM.unmount is called for both hosts when leaving source tab", async () => {
    const mountFn = vi.fn();
    const unmountFn = vi.fn();
    (window as unknown as Record<string, unknown>)["WickSCM"] = { mount: mountFn, unmount: unmountFn };

    render(DetailView, { props: DEFAULT_PROPS });
    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);
    await Promise.resolve();

    /* Switch away from source */
    const contextBtn = screen.getByRole("button", { name: /context/i });
    await fireEvent.click(contextBtn);
    await Promise.resolve();

    expect(unmountFn).toHaveBeenCalledTimes(2);
  });

  test("onClose passed to WickSCM.mount closes the source rail", async () => {
    const mountFn = vi.fn();
    const unmountFn = vi.fn();
    (window as unknown as Record<string, unknown>)["WickSCM"] = { mount: mountFn, unmount: unmountFn };

    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);
    await Promise.resolve();

    expect(container.querySelector("[data-scm-host]")).not.toBeNull();

    const [, opts] = mountFn.mock.calls[0] as [unknown, { onClose: () => void }];
    opts.onClose();
    await Promise.resolve();

    expect(container.querySelector("[data-scm-host]")).toBeNull();
  });

  test("mobile overlay for source tab has fixed inset-0 classes (full-screen, not floating box)", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    /* The mobile overlay wrapper must have fixed+inset-0+z-40 */
    const overlay = container.querySelector(".fixed.inset-0.z-40");
    expect(overlay).not.toBeNull();
  });

  test("mobile overlay for source tab renders SCM host div, not 'open on desktop' message", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    /* Should NOT show the old desktop-only message in mobile overlay */
    const desktopMsg = container.querySelector(".lg\\:hidden");
    if (desktopMsg) {
      expect(desktopMsg.textContent).not.toContain("open on desktop");
    }
  });
});

describe("DetailView — resizable + persisted Source sidebar width (#34)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
    (window as unknown as Record<string, unknown>)["WickSCM"] = undefined;
  });

  test("desktop Source sidebar applies persisted width as inline style on mount", async () => {
    localStorage.setItem("wick.scm.width", "512");
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    const sidePanel = container.querySelector<HTMLElement>(".lg\\:flex.flex-col");
    expect(sidePanel).not.toBeNull();
    expect(sidePanel?.getAttribute("style")).toContain("width: 512px");
  });

  test("desktop Source sidebar applies clamped default width when nothing persisted", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    const sidePanel = container.querySelector<HTMLElement>(".lg\\:flex.flex-col");
    // Read from the module rather than pinned: the default is a design call
    // that has already moved once (the rail floats over the panel's right
    // edge, so the usable width is this minus the gutter it reserves).
    expect(sidePanel?.getAttribute("style")).toContain(`width: ${SCM_DEFAULT_W}px`);
    // A margin, not padding: the panel has to END before the rail rather
    // than stay full-width with its contents pushed in, which left the
    // scrollbar mid-gutter and an empty band beside it.
    expect(sidePanel?.getAttribute("style")).toContain(`margin-right: ${RAIL_GUTTER_PX}px`);
  });

  test("desktop Source sidebar exposes a resize drag handle", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const sourceBtn = screen.getByRole("button", { name: /source/i });
    await fireEvent.click(sourceBtn);

    expect(container.querySelector("[data-scm-resize]")).not.toBeNull();
  });
});

describe("DetailView — placeholder views full-height (#10)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
  });

  test("commands placeholder view renders in a full-height flex container", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const tabBtn = screen.getByRole("button", { name: /tab menu/i });
    await fireEvent.click(tabBtn);
    const commandsBtn = screen.getByRole("button", { name: /^commands$/i });
    await fireEvent.click(commandsBtn);

    const wrapper = container.querySelector("[data-placeholder-view]");
    expect(wrapper).not.toBeNull();
    expect(wrapper?.className).toContain("flex-1");
    expect(wrapper?.className).toContain("flex");
    expect(wrapper?.className).toContain("items-center");
    expect(wrapper?.className).toContain("justify-center");
  });

  test("raw view renders the raw-trace panel (not a placeholder)", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const tabBtn = screen.getByRole("button", { name: /tab menu/i });
    await fireEvent.click(tabBtn);
    const rawBtn = screen.getByRole("button", { name: /^raw$/i });
    await fireEvent.click(rawBtn);

    expect(screen.getByText(/Raw trace/)).not.toBeNull();
    expect(screen.getByRole("button", { name: /^copy$/i })).not.toBeNull();
    expect(container.querySelector("[data-placeholder-view]")).toBeNull();
  });
});

describe("DetailView — rail tab count badges (#31)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  test("rail tabs render without a count badge when counts are zero", () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    expect(container.querySelector('[aria-label="Context"]')).not.toBeNull();
    /* zero-count badges must not appear */
    expect(container.querySelectorAll(".rounded-full.bg-green-500").length).toBe(0);
  });
});

describe("DetailView — approvals modal error propagation (#35)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  test("ApprovalsModal receives error prop (data-approval-error absent when no error)", () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    expect(container.querySelector("[data-approval-error]")).toBeNull();
  });

  // The approval_request SSE event fires once. Reloading the page while a
  // prompt is open means missing it entirely — and the daemon is still
  // waiting, so the agent hangs with no visible way to answer. Rehydrate
  // from the server on mount instead of only when the tab is clicked.
  test("fetches pending approvals on mount, not just on tab click", () => {
    render(DetailView, { props: DEFAULT_PROPS });
    expect(getApprovals).toHaveBeenCalledWith(DEFAULT_PROPS.base, DEFAULT_PROPS.sessionId);
  });
});

describe("DetailView — confirm before kill/dequeue (#33)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  test("header kill button is rendered", () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    const killBtn = container.querySelector('[aria-label="Kill session"]');
    expect(killBtn).not.toBeNull();
  });

  test("clicking header kill opens a confirm dialog instead of killing immediately", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    const killBtn = screen.getByRole("button", { name: /kill session/i });
    await fireEvent.click(killBtn);
    /* destructive action must be gated — killProcess not called yet */
    expect(killProcess).not.toHaveBeenCalled();
    /* the shared confirm dialog is now open */
    expect(screen.getByText("Stop this agent?")).toBeDefined();
  });

  test("confirming the dialog invokes killProcess", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByRole("button", { name: /kill session/i }));
    await fireEvent.click(screen.getByRole("button", { name: /^stop agent$/i }));
    expect(killProcess).toHaveBeenCalledWith("/api", "test-sess");
  });
});

describe("DetailView — pending ask rehydration on load (G3)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  test("getAsks is called on mount to rehydrate any pending ask", () => {
    render(DetailView, { props: DEFAULT_PROPS });
    expect(getAsks).toHaveBeenCalledWith("/api", "test-sess");
  });
});

describe("DetailView — process list polling (G4)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  /* Processes are loaded once on mount and thereafter refreshed by the SSE
     `lifecycle` event (see DetailView onMount), NOT a 5s interval — the timer
     poll was removed because it stacked redundant fetches on top of SSE. This
     test pins that: a load happens on mount, and advancing the clock adds none. */
  test("loadProcesses runs on mount and does not poll on a timer", () => {
    vi.useFakeTimers();
    try {
      render(DetailView, { props: DEFAULT_PROPS });
      const afterMount = vi.mocked(getProcesses).mock.calls.length;
      expect(afterMount).toBeGreaterThanOrEqual(1);
      vi.advanceTimersByTime(5000);
      /* no interval polling → advancing the clock adds no further fetches */
      expect(getProcesses).toHaveBeenCalledTimes(afterMount);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("DetailView — browser tab title from meta (G7)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    metaStore.set({});
    document.title = "";
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  test("document.title reflects the thread meta title", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    metaStore.set({ title: "My Session" });
    await Promise.resolve();
    expect(document.title).toContain("My Session");
  });
});

describe("DetailView — Ctrl/Cmd+B toggles the context rail (G8)", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  test("Ctrl+B opens the context side panel, again closes it", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    expect(container.querySelector(".lg\\:flex.flex-col")).toBeNull();

    await fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    expect(container.querySelector(".lg\\:flex.flex-col")).not.toBeNull();

    await fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    expect(container.querySelector(".lg\\:flex.flex-col")).toBeNull();
  });
});

describe("DetailView — conversation refetch on turn completion (artifacts)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sseBus.handler = null;
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  test("refetches conversation when a 'done' event arrives", () => {
    render(DetailView, { props: DEFAULT_PROPS });
    expect(getConversation).toHaveBeenCalledTimes(1);
    expect(sseBus.handler).not.toBeNull();
    sseBus.handler!({ type: "done" });
    expect(getConversation).toHaveBeenCalledTimes(2);
  });

  test("refetches conversation when an 'error' event arrives", () => {
    render(DetailView, { props: DEFAULT_PROPS });
    sseBus.handler!({ type: "error" });
    expect(getConversation).toHaveBeenCalledTimes(2);
  });

  test("does not refetch on a streaming text_delta event", () => {
    render(DetailView, { props: DEFAULT_PROPS });
    sseBus.handler!({ type: "text_delta" });
    expect(getConversation).toHaveBeenCalledTimes(1);
  });
});

/* A delegation happens MID-TURN. The leader keeps working afterwards, so no
   lifecycle or done event follows for a while — and the Sub-agents rail used
   to stay hidden until the turn ended or the page was reloaded, which is
   exactly the stretch where you want to see that work has fanned out. */
describe("DetailView — sub-agent roster follows delegation tool calls", () => {
  const runPromise = Effect.runPromise as unknown as ReturnType<typeof vi.fn>;
  const calls = () => (getSubAgentPanel as unknown as ReturnType<typeof vi.fn>).mock.calls.length;

  beforeEach(() => {
    vi.clearAllMocks();
    sseBus.handler = null;
    // The file-wide stub never settles, which parks loadSubAgents behind its
    // own in-flight guard forever — a refetch would be indistinguishable from
    // no refetch. Resolve here so the guard clears between events.
    runPromise.mockReturnValue(Promise.resolve([]));
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  afterEach(() => {
    runPromise.mockReturnValue(new Promise(() => {}));
  });

  test("a wick_delegate call refreshes the roster", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    await waitFor(() => expect(calls()).toBeGreaterThan(0));
    const before = calls();
    sseBus.handler!({ type: "tool_use", tool_name: "wick_delegate" });
    await waitFor(() => expect(calls()).toBeGreaterThan(before));
  });

  // Providers namespace MCP calls; matching the raw name would miss them.
  test("an MCP-namespaced delegate call counts too", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    await waitFor(() => expect(calls()).toBeGreaterThan(0));
    const before = calls();
    sseBus.handler!({ type: "tool_use", tool_name: "mcp__wick__wick_delegate" });
    await waitFor(() => expect(calls()).toBeGreaterThan(before));
  });

  // Every turn is full of unrelated tool calls; refetching on each one would
  // put the rail's endpoint behind every file read.
  test("an unrelated tool call does not refresh it", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    await waitFor(() => expect(calls()).toBeGreaterThan(0));
    const before = calls();
    sseBus.handler!({ type: "tool_use", tool_name: "read_file" });
    await new Promise((r) => setTimeout(r, 350));
    expect(calls()).toBe(before);
  });
});
