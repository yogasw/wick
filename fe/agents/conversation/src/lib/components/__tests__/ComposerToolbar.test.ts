import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ComposerToolbar from "../ComposerToolbar.svelte";
import type { ProviderOption, ProjectOption } from "../../types/agents.js";

const PROVIDERS: ProviderOption[] = [
  // Default instance: name === type.
  { type: "codex", name: "codex", version: "1.0" },
  // Named instance: name differs from type.
  { type: "codex", name: "gemini_flash", version: "1.0" },
  { type: "gemini", name: "gemini", version: "1.0" },
];

const PROJECTS: ProjectOption[] = [
  { id: "proj-1", name: "My Project", path: "/home/user/project", managed: false, pinned: false },
  { id: "proj-2", name: "Other Project", path: "/home/user/other", managed: false, pinned: false },
];

describe("ComposerToolbar", () => {
  test("renders provider button with active provider label", () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    expect(screen.getByText("anthropic")).toBeDefined();
  });

  test("provider dropdown opens when provider button is clicked", async () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "codex",
        activeProjectId: null,
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    const providerBtn = screen.getByRole("button", { name: /select provider/i });
    await fireEvent.click(providerBtn);
    expect(screen.getByText("gemini_flash")).toBeDefined();
  });

  test("clicking a named instance sends the full type/name key", async () => {
    const onProviderChange = vi.fn();
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "codex",
        activeProjectId: null,
        onProviderChange,
        onProjectChange: vi.fn(),
      },
    });
    const providerBtn = screen.getByRole("button", { name: /select provider/i });
    await fireEvent.click(providerBtn);

    // The named codex instance: aria-label is "codex/gemini_flash".
    const namedOption = screen.getByRole("button", { name: "codex/gemini_flash" });
    await fireEvent.click(namedOption);

    expect(onProviderChange).toHaveBeenCalledOnce();
    expect(onProviderChange).toHaveBeenCalledWith("codex/gemini_flash");
  });

  test("clicking a default instance sends the bare type", async () => {
    const onProviderChange = vi.fn();
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "codex",
        activeProjectId: null,
        onProviderChange,
        onProjectChange: vi.fn(),
      },
    });
    const providerBtn = screen.getByRole("button", { name: /select provider/i });
    await fireEvent.click(providerBtn);

    const geminiOption = screen.getByRole("button", { name: "gemini" });
    await fireEvent.click(geminiOption);

    expect(onProviderChange).toHaveBeenCalledOnce();
    expect(onProviderChange).toHaveBeenCalledWith("gemini");
  });

  test("renders project button with active project name", () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    expect(screen.getByText("My Project")).toBeDefined();
  });

  test("clicking a project option calls onProjectChange with project id", async () => {
    const onProjectChange = vi.fn();
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange,
      },
    });
    const projectBtn = screen.getByRole("button", { name: /select project/i });
    await fireEvent.click(projectBtn);

    const otherProject = screen.getByRole("button", { name: /Other Project/i });
    await fireEvent.click(otherProject);

    expect(onProjectChange).toHaveBeenCalledOnce();
    expect(onProjectChange).toHaveBeenCalledWith("proj-2");
  });

  test("notification bell button is rendered", () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: null,
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    const bell = screen.getByRole("button", { name: /notifications/i });
    expect(bell).toBeDefined();
  });

  test("project dropdown shows (none) option", async () => {
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange: vi.fn(),
      },
    });
    const projectBtn = screen.getByRole("button", { name: /select project/i });
    await fireEvent.click(projectBtn);
    expect(screen.getByText(/no project/i)).toBeDefined();
  });

  test("clicking (none) project option calls onProjectChange with null", async () => {
    const onProjectChange = vi.fn();
    render(ComposerToolbar, {
      props: {
        providers: PROVIDERS,
        projects: PROJECTS,
        activeProvider: "anthropic",
        activeProjectId: "proj-1",
        onProviderChange: vi.fn(),
        onProjectChange,
      },
    });
    const projectBtn = screen.getByRole("button", { name: /select project/i });
    await fireEvent.click(projectBtn);

    const noneBtn = screen.getByRole("button", { name: /no project/i });
    await fireEvent.click(noneBtn);

    expect(onProjectChange).toHaveBeenCalledWith(null);
  });
});

vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}));

describe("ComposerToolbar bell toggle", () => {
  const NOTIFY_KEY = "wick.conv.notify";

  const defaultProps = {
    providers: [],
    projects: [],
    activeProvider: null,
    activeProjectId: null,
    onProviderChange: vi.fn(),
    onProjectChange: vi.fn(),
  };

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    Object.defineProperty(window, "Notification", {
      value: { permission: "default", requestPermission: vi.fn().mockResolvedValue("default") },
      writable: true,
      configurable: true,
    });
  });

  test("bell click when pref off and permission granted enables notifications", async () => {
    const { toastOk } = await import("@wick-fe/common-stores");
    Object.defineProperty(window, "Notification", {
      value: { permission: "granted", requestPermission: vi.fn() },
      writable: true,
      configurable: true,
    });

    render(ComposerToolbar, { props: defaultProps });

    const bell = screen.getByRole("button", { name: /notifications/i });
    await fireEvent.click(bell);

    expect(localStorage.getItem(NOTIFY_KEY)).toBe("true");
    expect(toastOk).toHaveBeenCalledWith("Notifications enabled");
  });

  test("bell click when pref is on mutes notifications", async () => {
    const { toastOk } = await import("@wick-fe/common-stores");
    localStorage.setItem(NOTIFY_KEY, "true");
    Object.defineProperty(window, "Notification", {
      value: { permission: "granted", requestPermission: vi.fn() },
      writable: true,
      configurable: true,
    });

    render(ComposerToolbar, { props: defaultProps });

    const bell = screen.getByRole("button", { name: /notifications/i });
    await fireEvent.click(bell);

    expect(localStorage.getItem(NOTIFY_KEY)).toBe("false");
    expect(toastOk).toHaveBeenCalledWith("Notifications muted");
  });

  test("bell click when permission denied shows error toast", async () => {
    const { toastError } = await import("@wick-fe/common-stores");
    Object.defineProperty(window, "Notification", {
      value: { permission: "denied", requestPermission: vi.fn() },
      writable: true,
      configurable: true,
    });

    render(ComposerToolbar, { props: defaultProps });

    const bell = screen.getByRole("button", { name: /notifications/i });
    await fireEvent.click(bell);

    expect(localStorage.getItem(NOTIFY_KEY)).toBeNull();
    expect(toastError).toHaveBeenCalled();
  });

  test("bell click when permission default and resolves granted enables notifications", async () => {
    const { toastOk } = await import("@wick-fe/common-stores");
    Object.defineProperty(window, "Notification", {
      value: {
        permission: "default",
        requestPermission: vi.fn().mockResolvedValue("granted"),
      },
      writable: true,
      configurable: true,
    });

    render(ComposerToolbar, { props: defaultProps });

    const bell = screen.getByRole("button", { name: /notifications/i });
    await fireEvent.click(bell);
    await new Promise((r) => setTimeout(r, 0));

    expect(localStorage.getItem(NOTIFY_KEY)).toBe("true");
    expect(toastOk).toHaveBeenCalledWith("Notifications enabled");
  });

  test("green dot is visible when notifyOn is true", async () => {
    localStorage.setItem(NOTIFY_KEY, "true");

    const { container } = render(ComposerToolbar, { props: defaultProps });

    const dot = container.querySelector(".bg-green-500.rounded-full");
    expect(dot).not.toBeNull();
  });

  test("green dot is absent when notifyOn is false", async () => {
    localStorage.setItem(NOTIFY_KEY, "false");

    const { container } = render(ComposerToolbar, { props: defaultProps });

    const dot = container.querySelector(".bg-green-500.rounded-full");
    expect(dot).toBeNull();
  });
});
