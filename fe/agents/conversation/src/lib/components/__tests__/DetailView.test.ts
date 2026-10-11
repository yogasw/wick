import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

// A stand-in Effect: `.pipe(...)` returns itself so options.ts's builders
// (apiGetE(...).pipe(Effect.map(...))) don't throw at module load. Nothing
// runs it — Effect.runPromise is stubbed to a never-resolving promise below.
const inertEffect: { pipe: (...a: unknown[]) => unknown } = { pipe: () => inertEffect };

vi.mock("@wick-fe/common-api", () => ({
  WickClientLayer: {},
  APIError: class APIError extends Error {
    constructor(public readonly status: number, public readonly body: string) { super(body); }
  },
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
  pushToast: vi.fn(() => 1),
  dismissToast: vi.fn(),
}));

vi.mock("effect", () => ({
  Effect: {
    runPromise: vi.fn().mockReturnValue(new Promise(() => {})),
    provide: vi.fn((eff: unknown) => eff),
    map: vi.fn(() => (eff: unknown) => eff),
    either: vi.fn((eff: unknown) => ({ either: eff })),
  },
  Either: { isRight: (r: { _tag?: string }) => r?._tag === "Right" },
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

/* What the mocked thread store hands out at subscribe time. Tests that need
   a running or queued turn set these before render; afterEach resets them. */
const { threadState } = vi.hoisted(() => ({
  threadState: {
    typing: { active: false } as { active: boolean },
    lifecycle: { state: "", pid: 0, substate: "", at: 0 },
    turns: [] as unknown[],
    appendUserTurn: vi.fn(),
    renameTurn: vi.fn(),
    setHistory: vi.fn(),
  },
}));

/* The turns and live turn a test can push after render (the thread
   store's own updates); every other test only reads the initial value. */
const { threadBus } = vi.hoisted(() => {
  const bus = <T,>(initial: T) => {
    const subs = new Set<(v: T) => void>();
    let value = initial;
    return {
      subscribe(fn: (v: T) => void) { subs.add(fn); fn(value); return () => { subs.delete(fn); }; },
      set(v: T) { value = v; subs.forEach((fn) => fn(v)); },
      reset(v: T) { value = v; subs.clear(); },
    };
  };
  return { threadBus: { live: bus<unknown>(null), turns: bus<unknown[] | null>(null) } };
});

vi.mock("../../stores/thread.js", async (importActual) => ({
  isLocalTurnId: (await importActual<typeof import("../../stores/thread.js")>()).isLocalTurnId,
  createThreadStore: () => ({
    turns: { subscribe: (fn: (v: unknown[]) => void) => threadBus.turns.subscribe((v) => fn(v ?? threadState.turns)) },
    live: { subscribe: (fn: (v: null) => void) => threadBus.live.subscribe(fn as (v: unknown) => void) },
    typing: { subscribe: (fn: (v: { active: boolean }) => void) => { fn(threadState.typing); return () => {}; } },
    turnStartedAt: { subscribe: (fn: (v: number) => void) => { fn(0); return () => {}; } },
    contextUsed: { subscribe: (fn: (v: number) => void) => { fn(0); return () => {}; } },
    lifecycle: { subscribe: (fn: (v: { state: string; pid: number; substate: string; at: number }) => void) => { fn(threadState.lifecycle); return () => {}; } },
    meta: metaStore,
    cards: { subscribe: (fn: (v: Record<string, unknown>) => void) => { fn({}); return () => {}; }, set: vi.fn(), update: vi.fn() },
    setHistory: threadState.setHistory,
    appendUserTurn: threadState.appendUserTurn,
    renameTurn: threadState.renameTurn,
    handleEvent: vi.fn(),
    handleKilledLocally: vi.fn(),
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

vi.mock("../../api/context.js", () => ({
  fetchSessionContext: vi.fn(async () => ({
    session_id: "S1",
    used: 0,
    window: 0,
    pct: 0,
    turns: 0,
    totals: { input: 0, cache_read: 0, cache_write: 0, output: 0, total: 0, cost_usd: 0, cache_hit_pct: 0 },
    providers: [],
  })),
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
  searchMentionPaths: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
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

// tasksByTurn and heldForLiveTurn run as written; the spies count how
// often DetailView regroups the turns' tasks (not once per streamed token).
vi.mock("../../delegations.js", async (importActual) => {
  const actual = await importActual<typeof import("../../delegations.js")>();
  return { ...actual, tasksByTurn: vi.fn(actual.tasksByTurn), heldForLiveTurn: vi.fn(actual.heldForLiveTurn) };
});

import DetailView from "../DetailView.svelte";
import { tasksByTurn, heldForLiveTurn } from "../../delegations.js";
import { tick } from "svelte";
import { killProcess, getProcesses, dequeueProcess } from "../../api/processes.js";
import { getAsks } from "../../api/asks.js";
import { getApprovals } from "../../api/approvals.js";
import { fetchSessionContext } from "../../api/context.js";
import { getConversation } from "../../api/sessions.js";
import { toastWarn, pushToast } from "@wick-fe/common-stores";
import { APIError } from "@wick-fe/common-api";
import { getSubAgentPanel, interruptAllSubAgents } from "../../api/subagents.js";
import { SCM_DEFAULT_W, RAIL_GUTTER_PX } from "../../scmWidth.js";
import { Effect } from "effect";
import { sendMessage } from "../../api/messages.js";

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

    const contextBtn = screen.getByRole("button", { name: /files/i });
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
    const contextBtn = screen.getByRole("button", { name: /files/i });
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
    const contextBtn = screen.getByRole("button", { name: /files/i });
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
    expect(container.querySelector('[aria-label="Files"]')).not.toBeNull();
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

  /* The meter beside the composer is read once on mount and again on each
     lifecycle transition. Guarding the transitions with a seed of "" made
     the FIRST read look like a repeat — an idle session's lifecycle is ""
     — so the ring stayed blank and /context opened empty until the agent
     happened to move. */
  test("reads the context meter on mount, before anything happens", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    await waitFor(() =>
      expect(fetchSessionContext).toHaveBeenCalledWith(DEFAULT_PROPS.base, DEFAULT_PROPS.sessionId),
    );
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
  /* Rows the panel endpoint returns; null = the generic empty shape. */
  let liveRows: unknown[] | null = null;
  const calls = () => (getSubAgentPanel as unknown as ReturnType<typeof vi.fn>).mock.calls.length;

  beforeEach(() => {
    vi.clearAllMocks();
    sseBus.handler = null;
    // The file-wide stub never settles, which parks loadSubAgents behind its
    // own in-flight guard forever — a refetch would be indistinguishable from
    // no refetch. Resolve here so the guard clears between events.
    //
    // ONE stub answers every effect in this block, and each loader reads a
    // DIFFERENT field off the result, so no single literal fits: a bare []
    // left `subAgents` undefined and the derived busy-count threw on every
    // render — three unhandled errors that failed the workspace while all
    // 1019 tests passed, which reads as green to everything but the exit
    // code. Filling in the fields one at a time just moved the throw to the
    // next loader.
    //
    // So: an array whose every OTHER property is an empty array, with the
    // few that must not be a list named explicitly (`incident` especially —
    // [] is truthy and would render an incident that does not exist). New
    // loaders are covered without touching this.
    //
    // The real client normalises the shape (getSubAgentPanel maps
    // `r?.subagents ?? []`), but Effect.map is stubbed to identity here, so
    // it has to be right at the source.
    const notLists: Record<string, unknown> = {
      incident: null,
      schema: null,
      active: null,
      values: {},
      label: "",
      active_agent: "",
      provider: "",
      model_id: "",
      cwd: "",
      hopsLeft: 0,
      hasMore: false,
      truncated: false,
      deleted: false,
      always_approved: false,
      session_approved: false,
    };
    const anyShape = new Proxy([] as unknown[], {
      get(target, key) {
        if (key in target) return Reflect.get(target, key);
        if (typeof key !== "string") return undefined;
        if (key === "subAgents" && liveRows) return liveRows;
        return key in notLists ? notLists[key] : [];
      },
    });
    runPromise.mockReturnValue(Promise.resolve(anyShape));
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  afterEach(() => {
    liveRows = null;
    runPromise.mockReturnValue(new Promise(() => {}));
  });

  /* A child's lifecycle rides its own session id; the server re-addresses
     a sub_agent signal to the leader's stream (stream_subagent.go). */
  test("a sub_agent signal refreshes the roster", async () => {
    render(DetailView, { props: DEFAULT_PROPS });
    await waitFor(() => expect(calls()).toBeGreaterThan(0));
    const before = calls();
    sseBus.handler!({ type: "sub_agent", data: '{"child_session_id":"c1","state":"turn"}' } as { type: string });
    await waitFor(() => expect(calls()).toBeGreaterThan(before));
  });

  /* The old panel polled every 3 s while a sub-agent ran. With the stream
     up the signal covers it, so a running sub-agent issues no timer fetches. */
  test("a live sub-agent is not polled while the stream is connected", async () => {
    liveRows = [{ delegation_id: "d1", child_session_id: "c1", status: "running", lifecycle: "working", role: "x", task: "t" }];
    vi.useFakeTimers();
    try {
      render(DetailView, { props: DEFAULT_PROPS });
      await vi.advanceTimersByTimeAsync(1000);
      const before = calls();
      expect(before).toBeGreaterThan(0);
      await vi.advanceTimersByTimeAsync(5 * 60_000);
      expect(calls()).toBe(before);
    } finally {
      vi.useRealTimers();
    }
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

/* The composer's Stop: the only stop control in the Team app, where the
   header (and its Kill) is hidden. It must act without a confirm. */
describe("DetailView — composer Stop", () => {
  const runPromise = Effect.runPromise as unknown as ReturnType<typeof vi.fn>;
  const TEAM_PROPS = { ...DEFAULT_PROPS, agentMode: { hideHeader: true } };

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    if (!document.getElementById("app")) {
      const el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
  });

  afterEach(() => {
    threadState.typing = { active: false };
    threadState.lifecycle = { state: "", pid: 0, substate: "", at: 0 };
    runPromise.mockReturnValue(new Promise(() => {}));
  });

  test("no Stop while the agent is idle", () => {
    render(DetailView, { props: TEAM_PROPS });
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
  });

  test("Team mode: Stop shows while running and kills without a confirm", async () => {
    threadState.typing = { active: true };
    render(DetailView, { props: TEAM_PROPS as never });
    expect(screen.queryByRole("button", { name: /kill session/i })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(killProcess).toHaveBeenCalledWith("/api", "test-sess");
    expect(screen.queryByText("Stop this agent?")).toBeNull();
  });

  test("busy sub-agents: Stop asks first, and confirming stops them too", async () => {
    threadState.lifecycle = { state: "working", pid: 7, substate: "", at: 0 };
    const panel = { pipe: () => panel };
    (getSubAgentPanel as unknown as ReturnType<typeof vi.fn>).mockReturnValue(panel);
    runPromise.mockImplementation((e: unknown) =>
      e === panel
        ? Promise.resolve({ subAgents: [{ id: "d1", status: "running", lifecycle: "working" }], incident: null })
        : new Promise(() => {}),
    );
    render(DetailView, { props: TEAM_PROPS as never });
    await waitFor(() => expect(getSubAgentPanel).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    await fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(killProcess).not.toHaveBeenCalled();
    expect(screen.getByText("Stop this agent and its sub-agents?")).toBeDefined();
    await fireEvent.click(screen.getByRole("button", { name: /^stop all$/i }));
    expect(killProcess).toHaveBeenCalledWith("/api", "test-sess");
    expect(interruptAllSubAgents).toHaveBeenCalledWith("/api", "test-sess");
  });

  test("queued spawn: the button reads Cancel and dequeues", async () => {
    threadState.lifecycle = { state: "queued", pid: 0, substate: "", at: 0 };
    render(DetailView, { props: TEAM_PROPS as never });
    const btn = screen.getByRole("button", { name: "Cancel the queued agent" });
    await fireEvent.click(btn);
    expect(dequeueProcess).toHaveBeenCalledWith("/api", "test-sess");
    expect(killProcess).not.toHaveBeenCalled();
  });
});

describe("DetailView — reply chip", () => {
  const SEND = { send: true };
  const turn = {
    turn_id: "111", role: "assistant", agent: "main", provider: "", text: "Deploy finished",
    timestamp: 0, truncated: false, interrupted: false, has_trace: false, events: [], attachments: [],
  };
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    threadState.turns = [turn];
    threadState.appendUserTurn.mockReturnValue("local-user-1");
    vi.mocked(sendMessage).mockReturnValue({ pipe: () => SEND } as never);
    vi.mocked(Effect.runPromise).mockImplementation(((eff: unknown) =>
      eff === SEND ? Promise.resolve({ status: "queued", turn_id: "999" }) : new Promise(() => {})) as never);
  });
  afterEach(() => {
    threadState.turns = [];
    vi.mocked(sendMessage).mockReturnValue({ pipe: (x: unknown) => x } as never);
    vi.mocked(Effect.runPromise).mockReset();
    vi.mocked(Effect.runPromise).mockReturnValue(new Promise(() => {}));
  });

  async function pickReply() {
    render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(await screen.findByTestId("reply-action"));
    const chip = await screen.findByTestId("reply-chip");
    expect(chip.textContent).toContain("Replying to main");
    expect(chip.textContent).toContain("Deploy finished");
  }

  test("Reply sets the chip and × cancels it", async () => {
    await pickReply();
    await fireEvent.click(screen.getByTestId("reply-cancel"));
    expect(screen.queryByTestId("reply-chip")).toBeNull();
  });

  test("Esc in the composer cancels the chip", async () => {
    await pickReply();
    await fireEvent.keyDown(document.querySelector("textarea")!, { key: "Escape" });
    expect(screen.queryByTestId("reply-chip")).toBeNull();
  });

  test("a sent message carries reply_to, takes the server id, and clears the chip", async () => {
    await pickReply();
    const ta = document.querySelector("textarea")!;
    await fireEvent.input(ta, { target: { value: "roll it back" } });
    await fireEvent.keyDown(ta, { key: "Enter" });
    expect(vi.mocked(sendMessage)).toHaveBeenCalledWith("/api", "test-sess", expect.objectContaining({ text: "roll it back", reply_to: "111" }));
    expect(threadState.appendUserTurn).toHaveBeenCalledWith("roll it back", [], expect.objectContaining({ turn_id: "111" }));
    await waitFor(() => expect(screen.queryByTestId("reply-chip")).toBeNull());
    expect(threadState.renameTurn).toHaveBeenCalledWith("local-user-1", "999");
  });
});

/* P40: the strip prints the labels unless the profile turned them off. The
   choice arrives inlined on #app, like the rest of the rail layout. */
describe("DetailView — rail label style (P40)", () => {
  const app = () => {
    let el = document.getElementById("app");
    if (!el) {
      el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
    return el;
  };

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    app();
  });

  afterEach(() => {
    delete app().dataset.railPrefs;
  });

  const label = (container: HTMLElement) =>
    container.querySelector('[data-testid="rail-tab-files"] [class*="writing-mode"]');

  test("labels off: icons only, the name in an accessible tooltip", () => {
    app().dataset.railPrefs = JSON.stringify({ order: [], hidden: null, labels: false });
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    const btn = container.querySelector('[data-testid="rail-tab-files"]')!;
    expect(btn.getAttribute("aria-label")).toBe("Files");
    // No native title on top of the custom tooltip, or hover shows two.
    expect(btn.hasAttribute("title")).toBe(false);
    const tip = container.querySelector('[data-testid="rail-tip-files"]')!;
    expect(tip.textContent).toBe("Files");
    expect(tip.getAttribute("aria-hidden")).toBe("true");
    // Shown on keyboard focus too, not only on hover.
    expect(tip.className).toContain("group-focus-visible:opacity-100");
    expect(tip.className).toContain("group-hover:opacity-100");
    // Popover colours from the theme: light by default, navy only in dark.
    expect(tip.className).toContain("bg-white-100");
    expect(tip.className).toContain("dark:bg-navy-800");
    expect(tip.className).not.toMatch(/(^|\s)bg-navy-800/);
    expect(label(container)).toBeNull();
    // No element inside any tab carries a title either (Source used to).
    const strip = btn.parentElement!;
    expect(strip.querySelectorAll("[data-testid^='rail-tab-'] [title]").length).toBe(0);
  });

  const expectLabels = (container: HTMLElement) => {
    const btn = container.querySelector('[data-testid="rail-tab-files"]')!;
    expect(btn.getAttribute("aria-label")).toBe("Files");
    expect(btn.getAttribute("title")).toBe("Files");
    expect(label(container)?.textContent).toBe("Files");
    expect(container.querySelector('[data-testid="rail-tip-files"]')).toBeNull();
  };

  test("labels on: the name runs along the strip, no tooltip", () => {
    app().dataset.railPrefs = JSON.stringify({ order: [], hidden: null, labels: true });
    expectLabels(render(DetailView, { props: DEFAULT_PROPS }).container);
  });

  test("unset (saved layout without a choice): labels on", () => {
    app().dataset.railPrefs = JSON.stringify({ order: [], hidden: null });
    expectLabels(render(DetailView, { props: DEFAULT_PROPS }).container);
  });

  test("nothing inlined: labels on", () => {
    expectLabels(render(DetailView, { props: DEFAULT_PROPS }).container);
  });
});

/* P48 rail: Team tasks arriving over SSE draw the Tasks tab through
   RailTasksIcon — ring while one works, green count, amber dot while one
   waits for the person — and its icons-mode tooltip. */
describe("DetailView — Tasks rail marks (P48)", () => {
  const app = () => {
    let el = document.getElementById("app");
    if (!el) {
      el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
    return el;
  };

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    sseBus.handler = null;
    app();
  });

  afterEach(() => {
    delete app().dataset.railPrefs;
  });

  const task = (o: Record<string, unknown>) => ({
    type: "team_task",
    data: JSON.stringify({
      task_id: "t1", context_id: "c", to_agent_id: "a", to_handle: "franky", to_name: "Franky", title: "x",
      state: "working", turns: 1, max_turns: 8, started_at: "2026-10-10T10:00:00Z", updated_at: "2026-10-10T10:00:00Z", ...o,
    }),
  });

  test("1 working + 1 needs input: ring, green 2, amber dot, tooltip", async () => {
    app().dataset.railPrefs = JSON.stringify({ order: [], hidden: null, labels: false });
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    expect(container.querySelector('[data-testid="rail-tab-tasks"]')).toBeNull();
    sseBus.handler!(task({}) as { type: string });
    sseBus.handler!(task({ task_id: "t2", state: "input_required", needs_you: true }) as { type: string });
    await waitFor(() => expect(container.querySelector('[data-testid="rail-tab-tasks"]')).not.toBeNull());
    const tab = container.querySelector('[data-testid="rail-tab-tasks"]')!;
    expect(tab.getAttribute("aria-label")).toBe("Tasks · 2 active · 1 needs input");
    expect(tab.querySelector('[data-testid="rail-ring-tasks"]')?.className).toContain("animate-spin");
    const badge = tab.querySelector('[data-testid="rail-badge-tasks"]')!;
    expect(badge.textContent).toBe("2");
    expect(badge.className).toContain("bg-green-500");
    expect(badge.className).not.toContain("bg-amber-500");
    expect(tab.querySelector('[data-testid="rail-dot-tasks"]')?.className).toContain("bg-amber-500");
    const tip = tab.querySelector('[data-testid="rail-tip-tasks"]')!;
    expect(tip.textContent).toBe("Tasks · 2 active · 1 needs input");
    expect(tip.querySelector("b")?.textContent).toBe("Tasks");
    expect(tip.querySelector('[data-testid="rail-tip-tasks-needs"]')?.className).toContain("text-amber-700");
  });

  test("the mobile sheet titles the Tasks panel once, with the count", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    sseBus.handler!(task({}) as { type: string });
    sseBus.handler!(task({ task_id: "t2", state: "input_required", needs_you: true }) as { type: string });
    await waitFor(() => expect(container.querySelector('[data-testid="rail-tab-tasks"]')).not.toBeNull());
    await fireEvent.click(container.querySelector('[data-testid="rail-tab-tasks"]')!);
    const panels = await waitFor(() => {
      const p = container.querySelectorAll('[data-testid="tasks-panel"]');
      expect(p.length).toBe(2);
      return p;
    });
    const sheet = container.querySelector(".lg\\:hidden.fixed")!;
    const inSheet = [...panels].find((p) => sheet.contains(p))!;
    const docked = [...panels].find((p) => !sheet.contains(p))!;
    // Sheet: its own "Tasks" heading carries the count; the panel adds none.
    expect([...sheet.querySelectorAll("h2")].map((h) => h.textContent)).toEqual(["tasks"]);
    expect(sheet.querySelector('[data-testid="tasks-sheet-count"]')?.textContent).toBe("this chat · 2");
    expect(inSheet.querySelector("h2")).toBeNull();
    // Docked (lg+): the panel's own header.
    expect(docked.querySelector("h2")?.textContent).toBe("Tasks");
    expect(docked.querySelector('[data-testid="tasks-panel-count"]')?.textContent).toBe("this chat · 2");
  });
});

/* P48-s9: the running turn's delegation block, through DetailView's own
   wiring — tasks patched in by team_task, the live turn and its commit
   pushed through the thread store. */
describe("DetailView — live delegation block (P48-s9)", () => {
  const app = () => {
    let el = document.getElementById("app");
    if (!el) {
      el = document.createElement("div");
      el.id = "app";
      document.body.appendChild(el);
    }
    return el;
  };
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    sseBus.handler = null;
    threadBus.live.reset(null);
    threadBus.turns.reset(null);
    app();
  });
  afterEach(() => {
    threadBus.live.reset(null);
    threadBus.turns.reset(null);
  });

  const teamTask = (o: Record<string, unknown>) => ({
    type: "team_task",
    data: JSON.stringify({
      task_id: "k1", context_id: "c", to_agent_id: "a1", to_handle: "alpha", to_name: "Alpha", title: "x",
      state: "working", turns: 1, max_turns: 8, started_at: "2026-10-10T10:05:00Z", updated_at: "2026-10-10T10:05:00Z", ...o,
    }),
  }) as { type: string };
  const turn = (id: string, role: string, ts: string, text: string, events: unknown[] = []) => ({
    turn_id: id, role, agent: "", provider: "", text, ts, timestamp: Date.parse(ts), truncated: false, interrupted: false, has_trace: events.length > 0, events, attachments: [],
  });
  // An earlier turn sent "old"; the running one sends k1 and answers "old".
  const history = [
    turn("u1", "user", "2026-10-10T10:00:00Z", "first"),
    turn("a-old", "assistant", "2026-10-10T10:01:00Z", "asked beta", [
      { type: "tool_use", tool_use_id: "x1", tool_name: "mcp__wick__team_message", tool_input: '{"to":"@beta","message":"m"}' },
      { type: "tool_result", tool_use_id: "x1", text: '{"task_id":"old","state":"input_required"}' },
    ]),
    turn("u2", "user", "2026-10-10T10:04:00Z", "second"),
    // A turn woken in the same segment, before the running one: the
    // fallback would hand it k1 unless DetailView keeps live tasks out.
    turn("a-wake", "assistant", "2026-10-10T10:04:30Z", "noted"),
  ];
  const liveBlocks = [
    { kind: "tool", toolUseId: "l1", toolName: "mcp__wick__team_message", toolInput: '{"to":"@alpha","message":"m"}', result: '{"task_id":"k1","state":"working"}' },
    { kind: "tool", toolUseId: "l2", toolName: "mcp__wick__team_message", toolInput: '{"task_id":"old","message":"use the 20:55 one"}', result: '{"task_id":"old","state":"working"}' },
  ];
  const blocks = (c: HTMLElement) => [...c.querySelectorAll<HTMLElement>("[data-task-ids]")];
  const ownerOf = (el: HTMLElement) => el.closest<HTMLElement>("[data-turn-id]")?.dataset.turnId ?? "live";

  test("one block per turn through the live turn, a team_task patch and the commit", async () => {
    threadBus.turns.set(history);
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    sseBus.handler!(teamTask({ task_id: "old", to_handle: "beta", to_name: "Beta", state: "input_required", started_at: "2026-10-10T10:01:00Z" }));
    sseBus.handler!(teamTask({}));
    threadBus.live.set({ text: "", blocks: liveBlocks });
    await waitFor(() => expect(blocks(container).length).toBe(2));
    // The answer to "old" leaves it on the turn that sent it.
    expect(blocks(container).map((b) => [ownerOf(b), b.dataset.taskIds])).toEqual([["a-old", "old"], ["live", "k1"]]);

    // A team_task patch reaches the live block's chip.
    const liveChip = () => blocks(container).find((b) => ownerOf(b) === "live")!.textContent;
    expect(liveChip()).toContain("working");
    sseBus.handler!(teamTask({ state: "input_required", needs_you: true, updated_at: "2026-10-10T10:06:00Z" }));
    await waitFor(() => expect(liveChip()).toContain("needs input"));

    // Commit (thread store finalize): the live-* turn in the same update
    // that clears live.
    const committed = turn("live-1", "assistant", "2026-10-10T10:07:00Z", "waiting on alpha",
      liveBlocks.map((b) => ({ type: "tool_use", tool_name: b.toolName, tool_input: b.toolInput, tool_use_id: b.toolUseId, text: b.result })));
    threadBus.turns.set([...history, committed]);
    threadBus.live.set(null);
    await waitFor(() => expect(blocks(container).map((b) => [ownerOf(b), b.dataset.taskIds])).toEqual([["a-old", "old"], ["live-1", "k1"]]));
  });

  // P48-s18b: the team_task event ("sent") can reach the page before the
  // team_message result reaches the stream.
  const k1Owners = (c: HTMLElement) => blocks(c).filter((b) => b.dataset.taskIds?.split(" ").includes("k1")).map(ownerOf);

  test("a task sent before its tool_result shows only in the live block, then once on the commit", async () => {
    threadBus.turns.set(history);
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    threadBus.live.set({ text: "", blocks: [] });
    sseBus.handler!(teamTask({}));
    await waitFor(() => expect(container.querySelector('[data-testid="rail-tab-tasks"]')).not.toBeNull());
    await tick();
    // Not on the turn before (a-wake would take it by the fallback).
    expect(k1Owners(container)).toEqual([]);

    // The result arrives: the live block, once.
    threadBus.live.set({ text: "", blocks: [liveBlocks[0]] });
    await waitFor(() => expect(k1Owners(container)).toEqual(["live"]));

    const committed = turn("live-1", "assistant", "2026-10-10T10:07:00Z", "waiting on alpha",
      [liveBlocks[0]].map((b) => ({ type: "tool_use", tool_name: b.toolName, tool_input: b.toolInput, tool_use_id: b.toolUseId, text: b.result })));
    threadBus.turns.set([...history, committed]);
    threadBus.live.set(null);
    await waitFor(() => expect(k1Owners(container)).toEqual(["live-1"]));
  });

  test("streamed tokens do not regroup the turns' tasks", async () => {
    threadBus.turns.set(history);
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    sseBus.handler!(teamTask({}));
    threadBus.live.set({ text: "", blocks: liveBlocks });
    await waitFor(() => expect(k1Owners(container)).toEqual(["live"]));
    const calls = vi.mocked(tasksByTurn).mock.calls.length;
    const held = vi.mocked(heldForLiveTurn).mock.calls.length;
    expect(held).toBeGreaterThan(0);
    for (const text of ["Wait", "Waiting on", "Waiting on alpha"]) {
      threadBus.live.set({ text, blocks: liveBlocks });
      await tick();
    }
    await waitFor(() => expect(container.textContent).toContain("Waiting on alpha"));
    expect(vi.mocked(tasksByTurn).mock.calls.length).toBe(calls);
    expect(vi.mocked(heldForLiveTurn).mock.calls.length).toBe(held);
    expect(k1Owners(container)).toEqual(["live"]);
  });
});

/* P50: a reply quote whose original is not on screen fetches ONE window
   around it and shows it detached from the latest turns. */
describe("DetailView — reply jump (P50)", () => {
  const mk = (turn_id: string, text: string, extra: Record<string, unknown> = {}) => ({
    turn_id, role: "assistant", agent: "main", provider: "", text,
    timestamp: 0, truncated: false, interrupted: false, has_trace: false, events: [], attachments: [], ...extra,
  });
  const reply = (to: string) => mk("222", "roll it back", {
    role: "user", reply_to: { turn_id: to, role: "assistant", author: "main", excerpt: "Deploy finished" },
  });
  type Conv = { conv: { around?: string; after?: string; limit?: number } };
  let aroundResult: (opts: Conv["conv"]) => unknown;

  beforeEach(() => {
    vi.clearAllMocks();
    sseBus.handler = null;
    threadBus.turns.reset(null);
    Element.prototype.scrollIntoView = vi.fn();
    if (!(globalThis as { CSS?: unknown }).CSS) (globalThis as { CSS?: unknown }).CSS = { escape: (v: string) => v };
    // getConversation(...).pipe(provide) → a tagged request the runner reads.
    vi.mocked(getConversation).mockImplementation(((_b: string, _s: string, opts: Conv["conv"] = {}) =>
      ({ pipe: () => ({ conv: opts }) })) as never);
    aroundResult = (opts) => ({
      _tag: "Right",
      right: { turns: [mk("100", "older"), mk(opts.around!, "Deploy finished")], hasMore: true, hasMoreAfter: true },
    });
    vi.mocked(Effect.runPromise).mockImplementation(((eff: { either?: Conv }) =>
      eff?.either?.conv?.around ? Promise.resolve(aroundResult(eff.either.conv)) : new Promise(() => {})) as never);
  });
  afterEach(() => {
    threadState.turns = [];
    threadBus.turns.reset(null);
    vi.mocked(getConversation).mockReset();
    vi.mocked(getConversation).mockReturnValue({ pipe: (x: unknown) => x } as never);
    vi.mocked(Effect.runPromise).mockReset();
    vi.mocked(Effect.runPromise).mockReturnValue(new Promise(() => {}));
  });
  const aroundCalls = () => vi.mocked(getConversation).mock.calls.filter((c) => (c[2] as Conv["conv"])?.around);

  test("original on screen: scrolls to it, no request, Back to reply appears", async () => {
    threadState.turns = [mk("111", "Deploy finished"), reply("111")];
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    const target = container.querySelector('[data-turn-id="111"]')!;
    expect(vi.mocked(Element.prototype.scrollIntoView).mock.contexts[0]).toBe(target);
    expect(aroundCalls()).toHaveLength(0);
    expect(await screen.findByTestId("back-to-reply")).toBeDefined();
  });

  test("original not on screen: one around request, detached window, then Back / Jump to latest", async () => {
    threadState.turns = [reply("old")];
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    await waitFor(() => expect(container.querySelector('[data-turn-id="old"]')).not.toBeNull());
    expect(aroundCalls()).toHaveLength(1);
    expect(aroundCalls()[0][2]).toEqual({ around: "old" });
    // Detached: the window replaced the latest turns, Jump to latest shows.
    expect(container.querySelector('[data-turn-id="222"]')).toBeNull();
    expect(screen.getByTestId("jump-latest")).toBeDefined();

    // Live events only bump the badge; the window stays as it is.
    threadBus.turns.set([reply("old"), mk("333", "new reply")]);
    expect((await screen.findByTestId("jump-latest-badge")).textContent).toBe("1");
    expect(container.querySelector('[data-turn-id="333"]')).toBeNull();

    // Back to reply: its bubble was dropped with the latest window → around=222.
    await fireEvent.click(screen.getByTestId("back-to-reply"));
    await waitFor(() => expect(aroundCalls()).toHaveLength(2));
    expect(aroundCalls()[1][2]).toEqual({ around: "222" });
    await waitFor(() => expect(screen.queryByTestId("back-to-reply")).toBeNull());

    // Jump to latest leaves the window and reloads the latest one.
    const before = vi.mocked(getConversation).mock.calls.length;
    await fireEvent.click(screen.getByTestId("jump-latest"));
    await waitFor(() => expect(container.querySelector('[data-turn-id="333"]')).not.toBeNull());
    expect(vi.mocked(getConversation).mock.calls.length).toBe(before + 1);
    expect(vi.mocked(getConversation).mock.calls.at(-1)![2]).toEqual({ limit: 20 });
  });

  test("Jump to latest hides the Back to reply chip", async () => {
    threadState.turns = [reply("old")];
    render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    await screen.findByTestId("back-to-reply");
    await fireEvent.click(screen.getByTestId("jump-latest"));
    await waitFor(() => expect(screen.queryByTestId("back-to-reply")).toBeNull());
  });

  test("original gone: toast, no detached window", async () => {
    threadState.turns = [reply("gone")];
    aroundResult = () => ({ _tag: "Left", left: new (APIError as unknown as new (s: number, b: string) => Error)(404, "turn not found") });
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    await waitFor(() => expect(toastWarn).toHaveBeenCalledWith("Original message is no longer available"));
    expect(container.querySelector('[data-turn-id="222"]')).not.toBeNull();
    expect(screen.queryByTestId("back-to-reply")).toBeNull();
  });

  test("in flight: the quote spins and repeat clicks send no extra request", async () => {
    threadState.turns = [reply("old")];
    vi.mocked(Effect.runPromise).mockImplementation((() => new Promise(() => {})) as never);
    render(DetailView, { props: DEFAULT_PROPS });
    const q = screen.getByTestId("reply-quote");
    await fireEvent.click(q);
    await fireEvent.click(q);
    await fireEvent.click(q);
    expect(aroundCalls()).toHaveLength(1);
    expect(await screen.findByTestId("reply-quote-spinner")).toBeDefined();
    expect(q.getAttribute("title")).toBe("Finding message…");
    // Slow: a neutral "working" toast, not a success one.
    await waitFor(() => expect(pushToast).toHaveBeenCalledWith(expect.objectContaining({ state: "info", title: "Finding message…" })), { timeout: 1000 });
  });

  test("a streaming reply bumps the badge while detached", async () => {
    threadState.turns = [reply("old")];
    render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    await screen.findByTestId("back-to-reply");
    expect(screen.queryByTestId("jump-latest-badge")).toBeNull();
    threadBus.live.set({ turnId: "live-1", text: "typing", blocks: [] });
    expect((await screen.findByTestId("jump-latest-badge")).textContent).toBe("1");
    threadBus.live.reset(null);
  });

  test("paging down to the latest turn re-attaches the thread and reloads it", async () => {
    threadState.turns = [reply("old")];
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    await waitFor(() => expect(container.querySelector('[data-turn-id="old"]')).not.toBeNull());
    vi.mocked(Effect.runPromise).mockImplementation(((eff: { conv?: Conv["conv"]; either?: Conv }) =>
      eff?.conv?.after
        ? Promise.resolve({ turns: [reply("old")], hasMore: false, hasMoreAfter: false })
        : new Promise(() => {})) as never);
    await fireEvent.click(screen.getByRole("button", { name: "Load newer messages" }));
    await waitFor(() => expect(threadState.setHistory).toHaveBeenCalled());
    expect(vi.mocked(getConversation).mock.calls.find((c) => (c[2] as Conv["conv"])?.after)![2]).toEqual({ limit: 20, after: "old" });
    expect(threadState.setHistory.mock.calls.at(-1)![0].map((t: { turn_id: string }) => t.turn_id)).toEqual(["100", "old", "222"]);
    // Attached again: the store's turns show, and the latest window is re-read.
    await waitFor(() => expect(container.querySelector('[data-turn-id="100"]')).toBeNull());
    expect(vi.mocked(getConversation).mock.calls.at(-1)![2]).toEqual({ limit: 20 });
  });

  test("Jump to latest while a lookup is out: its late answer does not re-detach", async () => {
    threadState.turns = [reply("old")];
    let resolve!: (v: unknown) => void;
    vi.mocked(Effect.runPromise).mockImplementation(((eff: { either?: Conv }) =>
      eff?.either?.conv?.around ? new Promise((r) => { resolve = r; }) : new Promise(() => {})) as never);
    const { container } = render(DetailView, { props: DEFAULT_PROPS });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    await screen.findByTestId("reply-quote-spinner");
    await fireEvent.keyDown(window, { key: "ArrowDown", ctrlKey: true });
    await waitFor(() => expect(screen.queryByTestId("reply-quote-spinner")).toBeNull());
    resolve(aroundResult({ around: "old" }));
    await new Promise((r) => setTimeout(r, 20));
    expect(container.querySelector('[data-turn-id="old"]')).toBeNull();
    expect(container.querySelector('[data-turn-id="222"]')).not.toBeNull();
    expect(screen.queryByTestId("back-to-reply")).toBeNull();
  });
});
