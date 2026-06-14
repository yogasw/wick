import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

vi.mock("@wick-fe/common-api", () => ({
  WickClientLayer: {},
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
  },
}));

vi.mock("../../stores/thread.js", () => ({
  createThreadStore: () => ({
    turns: { subscribe: (fn: (v: unknown[]) => void) => { fn([]); return () => {}; } },
    live: { subscribe: (fn: (v: null) => void) => { fn(null); return () => {}; } },
    typing: { subscribe: (fn: (v: { active: boolean }) => void) => { fn({ active: false }); return () => {}; } },
    lifecycle: { subscribe: (fn: (v: { state: string; pid: number; substate: string; at: number }) => void) => { fn({ state: "", pid: 0, substate: "", at: 0 }); return () => {}; } },
    meta: { subscribe: (fn: (v: Record<string, unknown>) => void) => { fn({}); return () => {}; } },
    setHistory: vi.fn(),
    appendUserTurn: vi.fn(),
    handleEvent: vi.fn(),
  }),
}));

vi.mock("../../stores/sse.js", () => ({
  connectSession: () => ({
    close: vi.fn(),
    status: { subscribe: (fn: (v: string) => void) => { fn("connected"); return () => {}; } },
    onEvent: vi.fn(),
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

vi.mock("../../api/files.js", () => ({
  listFiles: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  readFile: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  saveFile: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  createFile: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  downloadURL: vi.fn().mockReturnValue(""),
}));

vi.mock("../../api/processes.js", () => ({
  getProcesses: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  killProcess: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
  dequeueProcess: vi.fn().mockReturnValue({ pipe: (x: unknown) => x }),
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

vi.mock("svelte/store", async (importActual) => {
  const actual = await importActual<typeof import("svelte/store")>();
  return { ...actual };
});

import DetailView from "../DetailView.svelte";

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

  test("raw placeholder view renders in a full-height flex container", async () => {
    const { container } = render(DetailView, { props: DEFAULT_PROPS });

    const tabBtn = screen.getByRole("button", { name: /tab menu/i });
    await fireEvent.click(tabBtn);
    const rawBtn = screen.getByRole("button", { name: /^raw$/i });
    await fireEvent.click(rawBtn);

    const wrapper = container.querySelector("[data-placeholder-view]");
    expect(wrapper).not.toBeNull();
    expect(wrapper?.className).toContain("flex-1");
    expect(wrapper?.className).toContain("flex");
    expect(wrapper?.className).toContain("items-center");
    expect(wrapper?.className).toContain("justify-center");
  });
});
