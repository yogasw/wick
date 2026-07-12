import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import type { ProjectOption, ProviderOption, SessionListItem } from "../../types/agents.js";

vi.mock("../../router.js", () => ({
  push: vi.fn(),
}));

import ProjectLanding from "../ProjectLanding.svelte";

const PROJECT: ProjectOption = { id: "proj-42", name: "Acme API", path: "/managed/path", managed: true, pinned: false };

const PROVIDER: ProviderOption = { type: "anthropic", name: "Claude Sonnet", version: "claude-sonnet-4" };

function makeSession(id: string): SessionListItem {
  return {
    id,
    label: `Chat ${id}`,
    status: "idle",
    project_id: "proj-42",
    active_agent: "claude",
    created_at: "2026-01-01T00:00:00Z",
    last_active: "2026-01-02T00:00:00Z",
    lifecycle: "idle",
  };
}

describe("ProjectLanding — presentational rendering", () => {
  const baseProps = {
    base: "/tools/agents",
    project: PROJECT,
    providers: [PROVIDER],
    sessions: [makeSession("s1"), makeSession("s2"), makeSession("s3")],
    onPin: vi.fn(),
    onSelectSession: vi.fn(),
  };

  test("renders project name as heading", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByRole("heading", { name: "Acme API" })).toBeDefined();
  });

  test("renders chat count derived from sessions length", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByText(/3 chats/)).toBeDefined();
  });

  test("shows 'managed' when project.managed is true", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByText(/3 chats · managed/)).toBeDefined();
  });

  test("shows 'custom' when project.managed is false", () => {
    const customProject = { ...PROJECT, managed: false };
    render(ProjectLanding, { props: { ...baseProps, project: customProject } });
    expect(screen.getByText(/3 chats · custom/)).toBeDefined();
  });

  test("renders a Pin as default button", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByRole("button", { name: /pin as default/i })).toBeDefined();
  });

  test("renders a Settings link pointing to the project settings page", () => {
    const { container } = render(ProjectLanding, { props: baseProps });
    const link = container.querySelector(`a[href='/tools/agents/projects/proj-42']`);
    expect(link).not.toBeNull();
  });

  test("renders a session list item for each session", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByText("Chat s1")).toBeDefined();
    expect(screen.getByText("Chat s2")).toBeDefined();
    expect(screen.getByText("Chat s3")).toBeDefined();
  });

  test("shows 0 chats when sessions array is empty", () => {
    render(ProjectLanding, { props: { ...baseProps, sessions: [] } });
    expect(screen.getByText(/0 chats/)).toBeDefined();
  });

  test("Composer has the correct placeholder text (message textarea present)", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByPlaceholderText(/ask anything/i)).toBeDefined();
  });

  test("Composer renders the + menu button (attach + notifications live inside it)", () => {
    render(ProjectLanding, { props: baseProps });
    expect(screen.getByRole("button", { name: /^add$/i })).toBeDefined();
  });

  test("renders an 'All chats' back-link pointing to base/sessions", () => {
    render(ProjectLanding, { props: baseProps });
    const link = screen.getByRole("link", { name: /all chats/i });
    expect(link).toBeDefined();
    expect(link.getAttribute("href")).toBe("/tools/agents/sessions");
  });
});

describe("ProjectLanding — composer default provider (#983)", () => {
  const CLAUDE: ProviderOption = { type: "claude", name: "claude", version: "" };
  const CODEX: ProviderOption = { type: "codex", name: "codex", version: "" };

  function propsWith(defaultProvider: string | undefined) {
    return {
      base: "/tools/agents",
      project: { ...PROJECT, defaultProvider },
      providers: [CLAUDE, CODEX],
      sessions: [],
      onPin: vi.fn(),
      onSelectSession: vi.fn(),
    };
  }

  test("preselects the project's default provider (codex), not the first one", () => {
    render(ProjectLanding, { props: propsWith("codex") });
    expect(screen.getByRole("button", { name: "Provider" }).getAttribute("title")).toBe("codex");
  });

  test("falls back to the first provider when no project default is set", () => {
    render(ProjectLanding, { props: propsWith(undefined) });
    expect(screen.getByRole("button", { name: "Provider" }).getAttribute("title")).toBe("claude");
  });

  test("matches a default given as a bare type against a named instance", () => {
    const named: ProviderOption = { type: "codex", name: "prod", version: "" };
    render(ProjectLanding, {
      props: { ...propsWith("codex"), providers: [CLAUDE, named] },
    });
    expect(screen.getByRole("button", { name: "Provider" }).getAttribute("title")).toBe("codex · prod");
  });
});

describe("ProjectLanding — folder path in header (#41)", () => {
  test("project header shows the folder path", () => {
    const project = { id: "p1", name: "Proj", path: "/home/work/proj", managed: true };
    render(ProjectLanding, { props: { base: "/agents", project, providers: [], sessions: [], onPin: vi.fn(), onSelectSession: vi.fn() } });
    expect(screen.getByText("/home/work/proj")).toBeDefined();
  });
});

describe("ProjectLanding — SessionList reuse (#39)", () => {
  test("in-project session list renders lifecycle status badge", () => {
    const project = { id: "p1", name: "Proj", path: "/p", managed: true };
    const sessions = [{ id: "s1", label: "Chat A", status: "", project_id: "p1", active_agent: "", created_at: "", last_active: "", lifecycle: "working" }];
    render(ProjectLanding, { props: { base: "/agents", project, providers: [], sessions, onPin: vi.fn(), onSelectSession: vi.fn() } });
    expect(screen.getByText("working")).toBeDefined();
  });

  test("clicking a session row calls onSelectSession", async () => {
    const project = { id: "p1", name: "Proj", path: "/p", managed: true };
    const sessions = [{ id: "s1", label: "Chat A", status: "", project_id: "p1", active_agent: "", created_at: "", last_active: "", lifecycle: "" }];
    const onSelectSession = vi.fn();
    render(ProjectLanding, { props: { base: "/agents", project, providers: [], sessions, onPin: vi.fn(), onSelectSession } });
    await fireEvent.click(screen.getByTestId("session-row-s1"));
    expect(onSelectSession).toHaveBeenCalledWith("s1");
  });
});

describe("ProjectLanding — create-and-navigate on send", () => {
  let fetchSpy: ReturnType<typeof vi.fn>;
  let originalFetch: typeof fetch;

  beforeEach(() => {
    originalFetch = global.fetch;
    fetchSpy = vi.fn();
    global.fetch = fetchSpy;
    /* suppress window.location.href assignment in jsdom */
    Object.defineProperty(window, "location", {
      value: { href: "" },
      writable: true,
    });
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  test("sending a message POSTs FormData with project_id and provider to base + '/'", async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: true,
      redirected: false,
      url: "/tools/agents/sessions/new-sess",
    } as Response);

    render(ProjectLanding, {
      props: {
        base: "/tools/agents",
        project: PROJECT,
        providers: [PROVIDER],
        sessions: [],
        onPin: vi.fn(),
        onSelectSession: vi.fn(),
      },
    });

    const textarea = screen.getByPlaceholderText(/ask anything/i);
    await fireEvent.input(textarea, { target: { value: "Hello project" } });
    const sendBtn = screen.getByRole("button", { name: /send/i });
    await fireEvent.click(sendBtn);

    expect(fetchSpy).toHaveBeenCalledOnce();
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/tools/agents/");
    expect(init.method).toBe("POST");
    expect(init.body).toBeInstanceOf(FormData);
    const fd = init.body as FormData;
    expect(fd.get("message")).toBe("Hello project");
    expect(fd.get("project_id")).toBe("proj-42");
    // Full "type/name" key now (a named instance no longer collapses to bare type).
    expect(fd.get("provider")).toBe("anthropic/Claude Sonnet");
  });

  test("navigates to the returned URL after successful create", async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: true,
      redirected: false,
      url: "/tools/agents/sessions/new-sess",
    } as Response);

    render(ProjectLanding, {
      props: {
        base: "/tools/agents",
        project: PROJECT,
        providers: [PROVIDER],
        sessions: [],
        onPin: vi.fn(),
        onSelectSession: vi.fn(),
      },
    });

    const textarea = screen.getByPlaceholderText(/ask anything/i);
    await fireEvent.input(textarea, { target: { value: "Navigate me" } });
    await fireEvent.click(screen.getByRole("button", { name: /send/i }));

    await vi.waitFor(() => {
      expect(window.location.href).toBe("/tools/agents/sessions/new-sess");
    });
  });
});
